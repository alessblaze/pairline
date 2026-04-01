defmodule OmeglePhoenix.SessionManager do
  use GenServer

  @active_sessions_key "sessions:active"
  @session_fields [
    :id,
    :token,
    :ip,
    :status,
    :partner_id,
    :last_partner_id,
    :signaling_ready,
    :webrtc_started,
    :preferences,
    :created_at,
    :last_activity,
    :ban_status,
    :ban_reason
  ]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def get_session(session_id) when is_binary(session_id) do
    case OmeglePhoenix.Redis.command(["GET", session_key(session_id)]) do
      {:ok, nil} -> {:error, :not_found}
      {:ok, payload} -> decode_session(payload)
      _ -> {:error, :not_found}
    end
  end

  def get_session(_session_id), do: {:error, :not_found}

  def get_all_sessions do
    sessions =
      case OmeglePhoenix.Redis.command(["SMEMBERS", @active_sessions_key]) do
        {:ok, session_ids} when is_list(session_ids) ->
          session_ids
          |> Enum.map(&get_session/1)
          |> Enum.flat_map(fn
            {:ok, session} -> [{session.id, session}]
            _ -> []
          end)
          |> Map.new()

        _ ->
          %{}
      end

    {:ok, sessions}
  end

  def get_sessions_by_ip(ip) when is_binary(ip) do
    sessions =
      case OmeglePhoenix.Redis.command(["SMEMBERS", ip_sessions_key(ip)]) do
        {:ok, session_ids} when is_list(session_ids) ->
          session_ids
          |> Enum.map(&get_session/1)
          |> Enum.flat_map(fn
            {:ok, session} -> [session]
            _ -> []
          end)

        _ ->
          []
      end

    {:ok, sessions}
  end

  def get_sessions_by_ip(_ip), do: {:ok, []}

  def create_session(session_id, ip, preferences) do
    session_token = generate_session_token()
    now = System.system_time(:second)

    session = %{
      id: session_id,
      token: session_token,
      ip: ip,
      status: :waiting,
      partner_id: nil,
      last_partner_id: nil,
      signaling_ready: false,
      webrtc_started: false,
      preferences: normalize_preferences(preferences),
      created_at: now,
      last_activity: now,
      ban_status: false,
      ban_reason: nil
    }

    case persist_session(session) do
      :ok ->
        {:ok, session}

      {:error, reason} ->
        {:error, reason}
    end
  end

  def update_session(session_id, updates) do
    with {:ok, session} <- get_session(session_id) do
      normalized_updates = normalize_updates(updates)
      updated_session = Map.merge(session, normalized_updates) |> touch_last_activity()

      case persist_session(updated_session) do
        :ok -> {:ok, updated_session}
        {:error, reason} -> {:error, reason}
      end
    end
  end

  def delete_session(session_id) do
    case get_session(session_id) do
      {:ok, session} ->
        case OmeglePhoenix.RedisState.delete_session(session_id, session.ip) do
          {:ok, _} -> :ok
          {:error, reason} -> {:error, reason}
        end

      {:error, :not_found} = error ->
        error
    end
  end

  def emergency_ban(session_id, reason) do
    with {:ok, session} <- update_session(session_id, %{ban_status: true, ban_reason: reason, status: :disconnecting}) do
      OmeglePhoenix.Matchmaker.leave_queue(session_id)
      OmeglePhoenix.Router.notify_banned(session_id, reason)
      disconnect_partner(session)
      {:ok, session}
    end
  end

  def emergency_disconnect(session_id) do
    with {:ok, session} <- update_session(session_id, %{status: :disconnecting}) do
      OmeglePhoenix.Matchmaker.leave_queue(session_id)
      OmeglePhoenix.Router.notify_disconnect(session_id, "disconnected by administrator")
      disconnect_partner(session)
      {:ok, session}
    end
  end

  def emergency_ban_ip(ip, reason) do
    {:ok, sessions} = get_sessions_by_ip(ip)

    banned_sessions =
      sessions
      |> Enum.flat_map(fn session ->
        case emergency_ban(session.id, reason) do
          {:ok, _updated} -> [session.id]
          _ -> []
        end
      end)

    {:ok, banned_sessions}
  end

  def emergency_unban(session_id) do
    update_session(session_id, %{ban_status: false, ban_reason: nil})
  end

  def emergency_unban_ip(ip) do
    {:ok, sessions} = get_sessions_by_ip(ip)

    Enum.each(sessions, fn session ->
      _ = emergency_unban(session.id)
    end)

    :ok
  end

  def ip_ban_reason(ip) do
    case OmeglePhoenix.Redis.command(["GET", "ban:ip:#{ip}"]) do
      {:ok, nil} -> nil
      {:ok, reason} -> reason
      _ -> nil
    end
  end

  def pair_sessions(session1, session2) do
    common_interests = get_common_interests(session1.preferences, session2.preferences)

    updated_session1 =
      touch_last_activity(%{
        session1
        | status: :matched,
          partner_id: session2.id,
          last_partner_id: session2.id,
          signaling_ready: false,
          webrtc_started: false
      })

    updated_session2 =
      touch_last_activity(%{
        session2
        | status: :matched,
          partner_id: session1.id,
          last_partner_id: session1.id,
          signaling_ready: false,
          webrtc_started: false
      })

    case OmeglePhoenix.RedisState.pair_sessions(updated_session1, updated_session2, ttl_seconds(), 900) do
      {:ok, _} -> {:ok, updated_session1, updated_session2, common_interests}
      {:error, reason} -> {:error, reason}
    end
  end

  def reset_pair(session1, session2) do
    updated_session1 =
      touch_last_activity(%{
        session1
        | partner_id: nil,
          status: :waiting,
          signaling_ready: false,
          webrtc_started: false
      })

    updated_session2 =
      touch_last_activity(%{
        session2
        | partner_id: nil,
          status: :waiting,
          signaling_ready: false,
          webrtc_started: false
      })

    case OmeglePhoenix.RedisState.reset_pair(updated_session1, updated_session2, ttl_seconds()) do
      {:ok, _} -> {:ok, updated_session1, updated_session2}
      {:error, reason} -> {:error, reason}
    end
  end

  def cleanup_orphaned_session(session_id) do
    case OmeglePhoenix.RedisState.cleanup_orphaned_session(session_id) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  @impl true
  def init(_opts) do
    {:ok, %{}}
  end

  @impl true
  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state) do
    :ok
  end

  defp normalize_updates(updates) when is_map(updates) do
    updates
    |> Enum.reduce(%{}, fn {key, value}, acc ->
      atom_key = normalize_update_key(key)

      if atom_key do
        Map.put(acc, atom_key, normalize_field(atom_key, value))
      else
        acc
      end
    end)
  end

  defp normalize_updates(_updates), do: %{}

  defp normalize_field(:status, value) when is_binary(value), do: String.to_atom(value)
  defp normalize_field(:status, value) when is_atom(value), do: value
  defp normalize_field(:preferences, value), do: normalize_preferences(value)
  defp normalize_field(:signaling_ready, value), do: truthy?(value)
  defp normalize_field(:webrtc_started, value), do: truthy?(value)
  defp normalize_field(:ban_status, value), do: truthy?(value)
  defp normalize_field(_field, value), do: value

  defp normalize_update_key(key) when is_atom(key) and key in @session_fields, do: key

  defp normalize_update_key(key) when is_binary(key) do
    try do
      atom_key = String.to_existing_atom(key)
      if atom_key in @session_fields, do: atom_key, else: nil
    rescue
      ArgumentError -> nil
    end
  end

  defp normalize_update_key(_key), do: nil

  defp truthy?(true), do: true
  defp truthy?(1), do: true
  defp truthy?("true"), do: true
  defp truthy?("1"), do: true
  defp truthy?(_value), do: false

  defp touch_last_activity(session) do
    Map.put(session, :last_activity, System.system_time(:second))
  end

  defp disconnect_partner(%{partner_id: nil}), do: :ok

  defp disconnect_partner(session) do
    with {:ok, partner_session} <- get_session(session.partner_id),
         {:ok, _updated_session, _updated_partner} <- reset_pair(session, partner_session) do
      OmeglePhoenix.Router.notify_disconnect(session.partner_id, "partner disconnected")
    else
      _ -> :ok
    end
  end

  defp persist_session(session) do
    case OmeglePhoenix.RedisState.persist_session(session, ttl_seconds()) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  defp decode_session(payload) do
    with {:ok, raw} <- Jason.decode(payload),
         {:ok, session} <- deserialize_session(raw) do
      {:ok, session}
    else
      _ -> {:error, :not_found}
    end
  end

  defp deserialize_session(raw) when is_map(raw) do
    session =
      Enum.reduce(@session_fields, %{}, fn field, acc ->
        key = Atom.to_string(field)
        Map.put(acc, field, deserialize_field(field, Map.get(raw, key)))
      end)

    {:ok, session}
  end

  defp deserialize_session(_raw), do: {:error, :invalid}

  defp deserialize_field(:status, nil), do: :waiting
  defp deserialize_field(:status, value) when is_binary(value), do: String.to_atom(value)
  defp deserialize_field(:signaling_ready, value), do: truthy?(value)
  defp deserialize_field(:webrtc_started, value), do: truthy?(value)
  defp deserialize_field(:ban_status, value), do: truthy?(value)
  defp deserialize_field(:preferences, value), do: normalize_preferences(value)
  defp deserialize_field(_field, value), do: value

  defp normalize_preferences(preferences) when is_map(preferences) do
    %{
      "mode" => safe_string(Map.get(preferences, "mode", Map.get(preferences, :mode, "text")), "text"),
      "interests" =>
        safe_string(Map.get(preferences, "interests", Map.get(preferences, :interests, "")), "")
    }
  end

  defp normalize_preferences(_), do: %{"mode" => "text", "interests" => ""}

  defp safe_string(nil, default), do: default
  defp safe_string(value, _default) when is_binary(value), do: value
  defp safe_string(value, _default) when is_atom(value), do: Atom.to_string(value)
  defp safe_string(value, _default) when is_integer(value), do: Integer.to_string(value)

  defp safe_string(value, _default) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp safe_string(_value, default), do: default

  defp get_common_interests(p1, p2) do
    p1 = normalize_preferences(p1)
    p2 = normalize_preferences(p2)

    i1 = Map.get(p1, "interests", "") |> String.trim()
    i2 = Map.get(p2, "interests", "") |> String.trim()

    if i1 != "" and i2 != "" do
      set1 = parse_interests(i1)
      set2 = parse_interests(i2)
      MapSet.intersection(set1, set2) |> MapSet.to_list()
    else
      []
    end
  end

  defp parse_interests(str) do
    str
    |> safe_string("")
    |> String.slice(0, 500)
    |> String.downcase()
    |> String.split([",", ";"], trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.take(10)
    |> MapSet.new()
  end

  defp generate_session_token do
    :crypto.strong_rand_bytes(32)
    |> Base.url_encode64(padding: false)
  end

  defp ttl_seconds do
    OmeglePhoenix.Config.get_session_ttl()
  end

  defp session_key(session_id), do: "session:data:#{session_id}"
  defp ip_sessions_key(ip), do: "ip:#{ip}"
end
