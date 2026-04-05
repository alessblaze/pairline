defmodule OmeglePhoenix.Redis.AdminSubscriber do
  use GenServer
  require Logger

  defstruct [:stream, :group, :consumer]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    stream = OmeglePhoenix.Config.get_admin_stream()

    state = %{
      stream: stream,
      group: OmeglePhoenix.Config.get_admin_stream_group(),
      consumer: stream_consumer_name()
    }

    send(self(), :connect)
    {:ok, state}
  end

  @impl true
  def handle_info(:connect, state) do
    case ensure_stream_group(state.stream, state.group) do
      :ok ->
        claim_stale_pending(state.stream, state.group, state.consumer)
        cleanup_stale_consumers(state.stream, state.group, state.consumer)
        send(self(), :consume_stream)
        {:noreply, state}

      {:error, reason} ->
        Logger.error(
          "Failed to initialize admin stream #{state.stream} / #{state.group}: #{inspect(reason)}"
        )

        Process.send_after(self(), :connect, 1_000)
        {:noreply, state}
    end
  end

  def handle_info(:consume_stream, state) do
    state =
      case consume_stream_entries(state) do
        :ok ->
          state

        {:error, reason} ->
          Logger.warning("Redis admin stream consumer disconnected: #{inspect(reason)}")
          Process.send_after(self(), :connect, 1_000)
          state
      end

    send(self(), :consume_stream)

    {:noreply, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state), do: :ok

  defp ensure_stream_group(stream, group) do
    case OmeglePhoenix.Redis.command(["XGROUP", "CREATE", stream, group, "$", "MKSTREAM"]) do
      {:ok, "OK"} ->
        :ok

      {:error, %Redix.Error{message: <<"BUSYGROUP", _::binary>>}} ->
        :ok

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp claim_stale_pending(stream, group, consumer) do
    do_claim_stale_pending(stream, group, consumer, "0-0", 0)
  end

  defp do_claim_stale_pending(_stream, _group, _consumer, _start_id, attempts)
       when attempts >= 100 do
    :ok
  end

  defp do_claim_stale_pending(stream, group, consumer, start_id, attempts) do
    case OmeglePhoenix.Redis.command([
           "XAUTOCLAIM",
           stream,
           group,
           consumer,
           "30000",
           start_id,
           "COUNT",
           "100"
         ]) do
      {:ok, [next_start_id, entries]} when is_binary(next_start_id) and is_list(entries) ->
        if entries == [] or next_start_id == start_id do
          :ok
        else
          do_claim_stale_pending(stream, group, consumer, next_start_id, attempts + 1)
        end

      {:ok, [next_start_id, entries, _deleted_ids]}
      when is_binary(next_start_id) and is_list(entries) ->
        if entries == [] or next_start_id == start_id do
          :ok
        else
          do_claim_stale_pending(stream, group, consumer, next_start_id, attempts + 1)
        end

      {:error, _} ->
        :ok

      _ ->
        :ok
    end
  end

  defp cleanup_stale_consumers(stream, group, current_consumer) do
    active_consumers = active_consumer_names(current_consumer)
    idle_cutoff_ms = OmeglePhoenix.Config.get_stream_stale_consumer_idle_ms()

    case OmeglePhoenix.Redis.command(["XINFO", "CONSUMERS", stream, group]) do
      {:ok, consumers} when is_list(consumers) ->
        Enum.each(consumers, fn consumer_info ->
          info = xinfo_to_map(consumer_info)
          name = Map.get(info, "name")
          idle_ms = xinfo_integer(info, "idle")

          if is_binary(name) and name != current_consumer and idle_ms >= idle_cutoff_ms and
               not MapSet.member?(active_consumers, name) do
            _ = OmeglePhoenix.Redis.command(["XGROUP", "DELCONSUMER", stream, group, name])
          end
        end)

      _ ->
        :ok
    end
  end

  defp consume_stream_entries(state) do
    with :ok <- consume_pending_entries(state),
         {:ok, entries} <- read_stream(state, ">") do
      Enum.each(entries, &handle_stream_entry(state.stream, &1))
      _ = ack_stream_entries(state, entries)
      :ok
    end
  end

  defp consume_pending_entries(state) do
    case read_stream(state, "0") do
      {:ok, []} ->
        :ok

      {:ok, entries} ->
        Enum.each(entries, &handle_stream_entry(state.stream, &1))
        ack_stream_entries(state, entries)

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp read_stream(state, stream_id) do
    command = [
      "XREADGROUP",
      "GROUP",
      state.group,
      state.consumer,
      "COUNT",
      Integer.to_string(OmeglePhoenix.Config.get_admin_stream_batch_size()),
      "BLOCK",
      Integer.to_string(OmeglePhoenix.Config.get_admin_stream_block_ms()),
      "STREAMS",
      state.stream,
      stream_id
    ]

    case OmeglePhoenix.Redis.command(command,
           timeout: OmeglePhoenix.Config.get_admin_stream_block_ms() + 2_000
         ) do
      {:ok, nil} ->
        {:ok, []}

      {:ok, [[_stream, entries]]} when is_list(entries) ->
        {:ok, entries}

      {:ok, _other} ->
        {:ok, []}

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp ack_stream_entries(_state, []), do: :ok

  defp ack_stream_entries(state, entries) do
    ids = Enum.map(entries, fn [entry_id, _fields] -> entry_id end)

    case OmeglePhoenix.Redis.command(["XACK", state.stream, state.group | ids]) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  defp handle_stream_entry(stream, [entry_id, fields]) when is_list(fields) do
    data =
      fields
      |> Enum.chunk_every(2)
      |> Enum.reduce(%{}, fn
        [key, value], acc -> Map.put(acc, key, value)
        _pair, acc -> acc
      end)

    case Map.get(data, "payload") do
      nil ->
        Logger.error("Invalid admin stream entry on #{stream}: #{inspect({entry_id, data})}")

      message ->
        case Jason.decode(message) do
          {:ok, %{"action" => action} = decoded} ->
            handle_admin_action(action, decoded)

          _ ->
            Logger.error("Invalid admin stream payload: #{inspect(message)}")
        end
    end
  end

  defp handle_admin_action("emergency_ban", data) do
    session_id = Map.get(data, "session_id")
    reason = Map.get(data, "reason", "admin action")

    if session_id && uuid?(session_id) do
      OmeglePhoenix.SessionManager.emergency_ban(session_id, reason)
      Logger.info("Emergency ban: #{session_id} - #{reason}")
    else
      Logger.error("Emergency ban: missing or invalid session_id")
    end
  end

  defp handle_admin_action("emergency_ban_ip", data) do
    ip = Map.get(data, "ip")
    reason = Map.get(data, "reason", "admin action")

    if ip && valid_ip?(ip) do
      OmeglePhoenix.SessionManager.emergency_ban_ip(ip, reason)
      Logger.info("Emergency ban IP: #{ip} - #{reason}")
    else
      Logger.error("Emergency ban IP: missing or invalid ip")
    end
  end

  defp handle_admin_action("emergency_disconnect", data) do
    session_id = Map.get(data, "session_id")

    if session_id && uuid?(session_id) do
      OmeglePhoenix.SessionManager.emergency_disconnect(session_id)
      Logger.info("Emergency disconnect: #{session_id}")
    else
      Logger.error("Emergency disconnect: missing or invalid session_id")
    end
  end

  defp handle_admin_action("emergency_unban", data) do
    session_id = Map.get(data, "session_id")

    if session_id && uuid?(session_id) do
      OmeglePhoenix.SessionManager.emergency_unban(session_id)
      Logger.info("Emergency unban: #{session_id}")
    else
      Logger.error("Emergency unban: missing or invalid session_id")
    end
  end

  defp handle_admin_action("emergency_unban_ip", data) do
    ip = Map.get(data, "ip")

    if ip && valid_ip?(ip) do
      OmeglePhoenix.SessionManager.emergency_unban_ip(ip)
      Logger.info("Emergency unban IP: #{ip}")
    else
      Logger.error("Emergency unban IP: missing or invalid ip")
    end
  end

  defp handle_admin_action("server_shutdown", _data) do
    Logger.warning(
      "Server shutdown action received via Redis but rejected - not supported via pub/sub"
    )
  end

  defp handle_admin_action(action, data) do
    Logger.warning("Unknown admin action: #{action}, data: #{inspect(data)}")
  end

  defp uuid?(str) when is_binary(str) do
    Regex.match?(
      ~r/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/,
      str
    )
  end

  defp uuid?(_), do: false

  defp valid_ip?(str) when is_binary(str) do
    case :inet.parse_address(String.to_charlist(str)) do
      {:ok, _} -> true
      _ -> false
    end
  end

  defp valid_ip?(_), do: false

  defp stream_consumer_name do
    sanitize_node_name(Node.self())
  end

  defp active_consumer_names(current_consumer) do
    [Node.self() | Node.list()]
    |> Enum.map(&sanitize_node_name/1)
    |> Enum.concat([current_consumer])
    |> MapSet.new()
  end

  defp xinfo_to_map(list) when is_list(list) do
    cond do
      Enum.all?(list, &match?([_, _], &1)) ->
        Map.new(list)

      rem(length(list), 2) == 0 ->
        list
        |> Enum.chunk_every(2)
        |> Enum.reduce(%{}, fn
          [key, value], acc when is_binary(key) -> Map.put(acc, key, value)
          _, acc -> acc
        end)

      true ->
        %{}
    end
  end

  defp xinfo_to_map(_), do: %{}

  defp xinfo_integer(info, key) do
    case Map.get(info, key) do
      value when is_integer(value) ->
        value

      value when is_binary(value) ->
        case Integer.parse(value) do
          {parsed, _} -> parsed
          :error -> 0
        end

      _ ->
        0
    end
  end

  defp sanitize_node_name(node) do
    node
    |> Atom.to_string()
    |> String.replace(~r/[^a-zA-Z0-9:_-]/u, "_")
  end
end
