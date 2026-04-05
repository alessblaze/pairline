defmodule OmeglePhoenix.Redis.AdminSubscriber do
  use GenServer
  require Logger

  defstruct [:connection, :stream, :group, :consumer]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    stream = OmeglePhoenix.Config.get_admin_stream()

    state = %{
      stream: stream,
      group: stream_group_name(),
      consumer: stream_consumer_name(),
      connection: nil
    }

    send(self(), :connect)
    {:ok, state}
  end

  @impl true
  def handle_info(:connect, state) do
    stop_connection(state.connection)

    case start_connection() do
      {:ok, connection} ->
        case ensure_stream_group(connection, state.stream, state.group) do
          :ok ->
            claim_stale_pending(connection, state.stream, state.group, state.consumer)
            send(self(), :consume_stream)
            {:noreply, %{state | connection: connection}}

          {:error, reason} ->
            Logger.error(
              "Failed to initialize admin stream #{state.stream} / #{state.group}: #{inspect(reason)}"
            )

            stop_connection(connection)
            Process.send_after(self(), :connect, 1_000)
            {:noreply, %{state | connection: nil}}
        end

      {:error, reason} ->
        Logger.error("Failed to connect admin stream consumer: #{inspect(reason)}")
        Process.send_after(self(), :connect, 1_000)
        {:noreply, %{state | connection: nil}}
    end
  end

  def handle_info(:consume_stream, %{connection: nil} = state) do
    {:noreply, state}
  end

  def handle_info(:consume_stream, state) do
    state =
      case consume_stream_entries(state) do
        :ok ->
          state

        {:error, reason} ->
          Logger.warning("Redis admin stream consumer disconnected: #{inspect(reason)}")
          stop_connection(state.connection)
          Process.send_after(self(), :connect, 1_000)
          %{state | connection: nil}
      end

    if state.connection != nil do
      send(self(), :consume_stream)
    end

    {:noreply, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    stop_connection(state.connection)
    :ok
  end

  defp start_connection do
    opts = [
      host: OmeglePhoenix.Config.get_redis_host(),
      port: OmeglePhoenix.Config.get_redis_port()
    ]

    opts =
      case OmeglePhoenix.Config.get_redis_password() do
        nil -> opts
        password -> Keyword.put(opts, :password, password)
      end

    Redix.start_link(opts)
  end

  defp stop_connection(nil), do: :ok

  defp stop_connection(connection) do
    try do
      Redix.stop(connection)
    rescue
      _ -> :ok
    end
  end

  defp ensure_stream_group(connection, stream, group) do
    case Redix.command(connection, ["XGROUP", "CREATE", stream, group, "$", "MKSTREAM"]) do
      {:ok, "OK"} ->
        :ok

      {:error, %Redix.Error{message: <<"BUSYGROUP", _::binary>>}} ->
        :ok

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp claim_stale_pending(connection, stream, group, consumer) do
    case Redix.command(connection, [
           "XAUTOCLAIM",
           stream,
           group,
           consumer,
           "30000",
           "0-0",
           "COUNT",
           "100"
         ]) do
      {:ok, _} -> :ok
      {:error, _} -> :ok
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

    case Redix.command(state.connection, command, timeout: OmeglePhoenix.Config.get_admin_stream_block_ms() + 2_000) do
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

    case Redix.command(state.connection, ["XACK", state.stream, state.group | ids]) do
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

  defp stream_group_name do
    "admin:" <> sanitize_node_name(Node.self())
  end

  defp stream_consumer_name do
    sanitize_node_name(Node.self())
  end

  defp sanitize_node_name(node) do
    node
    |> Atom.to_string()
    |> String.replace(~r/[^a-zA-Z0-9:_-]/u, "_")
  end
end
