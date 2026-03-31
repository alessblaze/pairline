defmodule OmeglePhoenix.Redis do
  use GenServer
  require Logger

  defstruct pool: [], subscribers: %{}

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def command(cmd) do
    GenServer.call(__MODULE__, {:command, cmd})
  end

  def pipeline(commands) do
    GenServer.call(__MODULE__, {:pipeline, commands})
  end

  def publish(channel, message) do
    GenServer.call(__MODULE__, {:publish, channel, message})
  end

  def subscribe(channel) do
    GenServer.call(__MODULE__, {:subscribe, channel}, :infinity)
  end

  def unsubscribe(channel) do
    GenServer.call(__MODULE__, {:unsubscribe, channel})
  end

  @impl true
  def init(_opts) do
    host = OmeglePhoenix.Config.get_redis_host()
    port = OmeglePhoenix.Config.get_redis_port()
    password = OmeglePhoenix.Config.get_redis_password()
    admin_channel = OmeglePhoenix.Config.get_admin_channel()

    pool_size = 10
    pool = Enum.map(1..pool_size, fn _ -> connect(host, port, password) end)

    subscribers =
      case start_subscription(host, port, password, admin_channel) do
        {:ok, sub_conn} ->
          %{admin_channel => %{connection: sub_conn, channel: admin_channel}}

        {:error, reason} ->
          Logger.error(
            "Failed to subscribe to admin channel #{admin_channel}: #{inspect(reason)}"
          )

          %{}
      end

    {:ok, %__MODULE__{pool: pool, subscribers: subscribers}}
  end

  @impl true
  def handle_call({:command, cmd}, _from, state) do
    [conn | rest] = state.pool
    new_pool = rest ++ [conn]
    result = Redix.command(conn, cmd)
    {:reply, result, %{state | pool: new_pool}}
  end

  def handle_call({:pipeline, commands}, _from, state) do
    [conn | rest] = state.pool
    new_pool = rest ++ [conn]
    result = Redix.pipeline(conn, commands)
    {:reply, result, %{state | pool: new_pool}}
  end

  def handle_call({:publish, channel, message}, _from, state) do
    [conn | rest] = state.pool
    new_pool = rest ++ [conn]
    result = Redix.command(conn, ["PUBLISH", channel, Jason.encode!(message)])
    {:reply, result, %{state | pool: new_pool}}
  end

  def handle_call({:subscribe, channel}, {caller_pid, _tag}, state) do
    host = OmeglePhoenix.Config.get_redis_host()
    port = OmeglePhoenix.Config.get_redis_port()
    password = OmeglePhoenix.Config.get_redis_password()

    opts = [host: host, port: port]

    opts =
      if password do
        Keyword.put(opts, :password, password)
      else
        opts
      end

    case Redix.PubSub.start_link(opts) do
      {:ok, sub_conn} ->
        case Redix.PubSub.subscribe(sub_conn, channel, self()) do
          {:ok, _ref} ->
            new_subscribers =
              Map.put(state.subscribers, caller_pid, %{
                connection: sub_conn,
                channel: channel
              })

            {:reply, {:ok, sub_conn}, %{state | subscribers: new_subscribers}}

          {:error, reason} ->
            Redix.PubSub.stop(sub_conn)
            {:reply, {:error, reason}, state}
        end

      {:error, reason} ->
        {:reply, {:error, reason}, state}
    end
  end

  def handle_call({:unsubscribe, channel}, _from, state) do
    case find_subscriber_by_channel(channel, state.subscribers) do
      {:ok, pid, sub_data} ->
        conn = sub_data.connection
        Redix.PubSub.unsubscribe(conn, channel, self())
        Redix.PubSub.stop(conn)
        new_subscribers = Map.delete(state.subscribers, pid)
        {:reply, :ok, %{state | subscribers: new_subscribers}}

      :not_found ->
        {:reply, {:error, :not_found}, state}
    end
  end

  def handle_call(_request, _from, state) do
    {:reply, {:error, :unknown_request}, state}
  end

  @impl true
  def handle_cast(_msg, state) do
    {:noreply, state}
  end

  @impl true
  def handle_info({:redix_pubsub, _pid, _ref, :subscribed, _}, state) do
    {:noreply, state}
  end

  def handle_info({:redix_pubsub, _pid, _ref, :unsubscribed, _}, state) do
    {:noreply, state}
  end

  def handle_info(
        {:redix_pubsub, _pid, _ref, :message, %{channel: channel, payload: message}},
        state
      ) do
    handle_admin_message(channel, message)
    {:noreply, state}
  end

  def handle_info({:redix_pubsub, _pid, _ref, :disconnected, _}, state) do
    Logger.warning("Redis pub/sub disconnected")
    {:noreply, state}
  end

  def handle_info({:DOWN, _ref, :process, pid, _reason}, state) do
    case Map.get(state.subscribers, pid) do
      nil ->
        {:noreply, state}

      sub_data ->
        try do
          Redix.PubSub.stop(sub_data.connection)
        rescue
          _ -> :ok
        end

        new_subscribers = Map.delete(state.subscribers, pid)
        {:noreply, %{state | subscribers: new_subscribers}}
    end
  end

  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    Enum.each(state.pool, fn conn ->
      try do
        Redix.stop(conn)
      rescue
        _ -> :ok
      end
    end)

    Enum.each(state.subscribers, fn {_pid, sub_data} ->
      try do
        Redix.PubSub.stop(sub_data.connection)
      rescue
        _ -> :ok
      end
    end)

    :ok
  end

  defp connect(host, port, password) do
    opts = [host: host, port: port]

    opts =
      if password do
        Keyword.put(opts, :password, password)
      else
        opts
      end

    case Redix.start_link(opts) do
      {:ok, pid} -> pid
      {:error, reason} -> raise "Redis connection failed: #{inspect(reason)}"
    end
  end

  defp start_subscription(host, port, password, channel) do
    opts = [host: host, port: port]

    opts =
      if password do
        Keyword.put(opts, :password, password)
      else
        opts
      end

    with {:ok, sub_conn} <- Redix.PubSub.start_link(opts),
         {:ok, _ref} <- Redix.PubSub.subscribe(sub_conn, channel, self()) do
      {:ok, sub_conn}
    else
      {:error, _reason} = error ->
        error
    end
  end

  defp find_subscriber_by_channel(channel, subscribers) do
    Enum.find(subscribers, :not_found, fn {_pid, sub_data} ->
      sub_data.channel == channel
    end)
  end

  defp handle_admin_message("admin:action", message) do
    case Jason.decode(message) do
      {:ok, %{"action" => action} = data} ->
        handle_admin_action(action, data)

      _ ->
        Logger.error("Invalid admin message: #{message}")
    end
  end

  defp handle_admin_message(channel, message) do
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
    Logger.warning(
      "Server shutdown action received via Redis but rejected — not supported via pub/sub"
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
end
