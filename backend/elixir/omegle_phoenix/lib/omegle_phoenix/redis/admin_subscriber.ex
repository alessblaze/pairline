defmodule OmeglePhoenix.Redis.AdminSubscriber do
  use GenServer
  require Logger

  defstruct [:connection, :channel]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    channel = OmeglePhoenix.Config.get_admin_channel()
    state = %{channel: channel, connection: nil}
    send(self(), :connect)
    {:ok, state}
  end

  @impl true
  def handle_info(:connect, state) do
    case start_subscription(state.channel) do
      {:ok, connection} ->
        {:noreply, %{state | connection: connection}}

      {:error, reason} ->
        Logger.error("Failed to subscribe to admin channel #{state.channel}: #{inspect(reason)}")
        Process.send_after(self(), :connect, 1_000)
        {:noreply, state}
    end
  end

  def handle_info({:redix_pubsub, _pid, _ref, :message, %{channel: channel, payload: message}}, state) do
    handle_admin_message(channel, message, state.channel)
    {:noreply, state}
  end

  def handle_info({:redix_pubsub, _pid, _ref, :disconnected, _reason}, state) do
    Logger.warning("Redis admin pub/sub disconnected")
    Process.send_after(self(), :connect, 1_000)
    {:noreply, %{state | connection: nil}}
  end

  def handle_info({:redix_pubsub, _pid, _ref, _event, _payload}, state) do
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

  defp start_subscription(channel) do
    opts = [
      host: OmeglePhoenix.Config.get_redis_host(),
      port: OmeglePhoenix.Config.get_redis_port()
    ]

    opts =
      case OmeglePhoenix.Config.get_redis_password() do
        nil -> opts
        password -> Keyword.put(opts, :password, password)
      end

    with {:ok, sub_conn} <- Redix.PubSub.start_link(opts),
         {:ok, _ref} <- Redix.PubSub.subscribe(sub_conn, channel, self()) do
      {:ok, sub_conn}
    end
  end

  defp stop_connection(nil), do: :ok

  defp stop_connection(connection) do
    try do
      Redix.PubSub.stop(connection)
    rescue
      _ -> :ok
    end
  end

  defp handle_admin_message(channel, message, expected_channel) when channel == expected_channel do
    case Jason.decode(message) do
      {:ok, %{"action" => action} = data} ->
        handle_admin_action(action, data)

      _ ->
        Logger.error("Invalid admin message: #{message}")
    end
  end

  defp handle_admin_message(channel, message, _expected_channel) do
    Logger.debug("Unknown channel: #{channel}, message: #{message}")
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
    Logger.warning("Server shutdown action received via Redis but rejected - not supported via pub/sub")
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
end
