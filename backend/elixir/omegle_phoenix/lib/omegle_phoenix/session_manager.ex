defmodule OmeglePhoenix.SessionManager do
  use GenServer
  require Logger

  @table :session_manager_table

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def get_session(session_id) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, session}] -> {:ok, session}
      [] -> {:error, :not_found}
    end
  end

  def get_all_sessions do
    sessions =
      :ets.foldl(
        fn {_id, session}, acc -> Map.put(acc, session.id, session) end,
        %{},
        @table
      )

    {:ok, sessions}
  end

  def get_sessions_by_ip(ip) do
    sessions =
      :ets.foldl(
        fn {_id, session}, acc ->
          if session.ip == ip, do: [session | acc], else: acc
        end,
        [],
        @table
      )

    {:ok, sessions}
  end

  def create_session(session_id, ip, preferences) do
    GenServer.call(__MODULE__, {:create_session, session_id, ip, preferences})
  end

  def update_session(session_id, updates) do
    GenServer.call(__MODULE__, {:update_session, session_id, updates})
  end

  def delete_session(session_id) do
    GenServer.call(__MODULE__, {:delete_session, session_id})
  end

  def emergency_ban(session_id, reason) do
    GenServer.call(__MODULE__, {:emergency_ban, session_id, reason})
  end

  def emergency_disconnect(session_id) do
    GenServer.call(__MODULE__, {:emergency_disconnect, session_id})
  end

  def emergency_ban_ip(ip, reason) do
    GenServer.call(__MODULE__, {:emergency_ban_ip, ip, reason})
  end

  def emergency_unban(session_id) do
    GenServer.call(__MODULE__, {:emergency_unban, session_id})
  end

  def emergency_unban_ip(ip) do
    GenServer.call(__MODULE__, {:emergency_unban_ip, ip})
  end

  def ip_ban_reason(ip) do
    case OmeglePhoenix.Redis.command(["GET", "ban:ip:#{ip}"]) do
      {:ok, nil} -> nil
      {:ok, reason} -> reason
      _ -> nil
    end
  end

  @impl true
  def init(_opts) do
    table = :ets.new(@table, [:named_table, :protected, :set, read_concurrency: true])
    {:ok, %{table: table}}
  end

  @impl true
  def handle_call({:create_session, session_id, ip, preferences}, _from, state) do
    now = System.system_time(:second)
    session_token = generate_session_token()
    preferences = normalize_preferences(preferences)

    session = %{
      id: session_id,
      token: session_token,
      ip: ip,
      status: :waiting,
      partner_id: nil,
      last_partner_id: nil,
      signaling_ready: false,
      webrtc_started: false,
      preferences: preferences,
      created_at: now,
      last_activity: now,
      ban_status: false,
      ban_reason: nil
    }

    hashed_token = :crypto.hash(:sha256, session_token) |> Base.encode16(case: :lower)
    OmeglePhoenix.Redis.command(["SADD", "ip:#{ip}", session_id])
    OmeglePhoenix.Redis.command(["SETEX", "session:#{session_id}:ip", "86400", ip])
    OmeglePhoenix.Redis.command(["SETEX", "session:#{session_id}:token", "86400", hashed_token])

    :ets.insert(@table, {session_id, session})

    {:reply, {:ok, session}, state}
  end

  def handle_call({:update_session, session_id, updates}, _from, state) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, session}] ->
        updates =
          if is_map(updates) do
            case Map.fetch(updates, :preferences) do
              {:ok, prefs} -> Map.put(updates, :preferences, normalize_preferences(prefs))
              :error -> updates
            end
            |> then(fn updates2 ->
              case Map.fetch(updates2, "preferences") do
                {:ok, prefs} -> Map.put(updates2, "preferences", normalize_preferences(prefs))
                :error -> updates2
              end
            end)
          else
            %{}
          end

        updated_session = Map.merge(session, updates)
        :ets.insert(@table, {session_id, updated_session})
        {:reply, {:ok, updated_session}, state}

      [] ->
        {:reply, {:error, :not_found}, state}
    end
  end

  def handle_call({:delete_session, session_id}, _from, state) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, session}] ->
        ip = session.ip
        OmeglePhoenix.Redis.command(["SREM", "ip:#{ip}", session_id])
        OmeglePhoenix.Redis.command(["DEL", "session:#{session_id}:ip"])
        OmeglePhoenix.Redis.command(["DEL", "session:#{session_id}:token"])

        case OmeglePhoenix.Redis.command(["SCARD", "ip:#{ip}"]) do
          {:ok, "0"} ->
            OmeglePhoenix.Redis.command(["DEL", "ip:#{ip}"])

          _ ->
            :ok
        end

        :ets.delete(@table, session_id)
        {:reply, :ok, state}

      [] ->
        {:reply, {:error, :not_found}, state}
    end
  end

  def handle_call({:emergency_ban, session_id, reason}, _from, state) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, session}] ->
        updated_session = %{
          session
          | ban_status: true,
            ban_reason: reason,
            status: :disconnecting
        }

        :ets.insert(@table, {session_id, updated_session})

        case session.partner_id do
          nil ->
            :ok

          partner_id ->
            case :ets.lookup(@table, partner_id) do
              [{^partner_id, partner_session}] ->
                updated_partner = %{partner_session | status: :disconnecting}
                :ets.insert(@table, {partner_id, updated_partner})

              [] ->
                :ok
            end
        end

        {:reply, {:ok, updated_session}, state}

      [] ->
        {:reply, {:error, :not_found}, state}
    end
  end

  def handle_call({:emergency_disconnect, session_id}, _from, state) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, session}] ->
        updated_session = %{session | status: :disconnecting}
        :ets.insert(@table, {session_id, updated_session})

        case session.partner_id do
          nil ->
            :ok

          partner_id ->
            case :ets.lookup(@table, partner_id) do
              [{^partner_id, partner_session}] ->
                updated_partner = %{
                  partner_session
                  | status: :disconnecting,
                    partner_id: nil
                }

                :ets.insert(@table, {partner_id, updated_partner})

              [] ->
                :ok
            end

            :ets.insert(@table, {session_id, %{updated_session | partner_id: nil}})
        end

        {:reply, {:ok, updated_session}, state}

      [] ->
        {:reply, {:error, :not_found}, state}
    end
  end

  def handle_call({:emergency_ban_ip, ip, reason}, _from, state) do
    banned_sessions =
      :ets.foldl(
        fn {session_id, session}, acc ->
          if session.ip == ip do
            updated_session = %{
              session
              | ban_status: true,
                ban_reason: reason,
                status: :disconnecting
            }

            :ets.insert(@table, {session_id, updated_session})
            [session_id | acc]
          else
            acc
          end
        end,
        [],
        @table
      )

    {:reply, {:ok, banned_sessions}, state}
  end

  def handle_call({:emergency_unban, session_id}, _from, state) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, session}] ->
        updated_session = %{
          session
          | ban_status: false,
            ban_reason: nil
        }

        :ets.insert(@table, {session_id, updated_session})
        {:reply, {:ok, updated_session}, state}

      [] ->
        {:reply, {:error, :not_found}, state}
    end
  end

  def handle_call({:emergency_unban_ip, ip}, _from, state) do
    :ets.foldl(
      fn {session_id, session}, _acc ->
        if session.ip == ip do
          :ets.insert(@table, {session_id, %{session | ban_status: false, ban_reason: nil}})
        end

        :ok
      end,
      :ok,
      @table
    )

    {:reply, :ok, state}
  end

  def handle_call(_request, _from, state) do
    {:reply, {:error, :unknown_request}, state}
  end

  @impl true
  def handle_cast(_msg, state) do
    {:noreply, state}
  end

  @impl true
  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state) do
    :ok
  end

  defp normalize_preferences(preferences) when is_map(preferences) do
    %{
      "mode" => safe_string(Map.get(preferences, "mode", "text"), "text"),
      "interests" => safe_string(Map.get(preferences, "interests", ""), "")
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

  defp generate_session_token do
    :crypto.strong_rand_bytes(32)
    |> Base.url_encode64(padding: false)
  end
end
