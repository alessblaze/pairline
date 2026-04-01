defmodule OmeglePhoenix.Router do
  @moduledoc """
  Cluster-aware session event routing backed by Redis owner-node coordination.
  """

  use GenServer
  require Logger

  @reconnect_message :connect_router_channel

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def register(session_id, pid) when is_binary(session_id) and is_pid(pid) do
    if pid != self() do
      {:error, :must_register_from_owner_process}
    else
      :ok = Phoenix.PubSub.subscribe(OmeglePhoenix.PubSub, topic(session_id))
      refresh_owner(session_id, pid)
    end
  end

  def refresh_owner(session_id, pid) when is_binary(session_id) and is_pid(pid) do
    if pid != self() do
      {:error, :must_refresh_from_owner_process}
    else
      OmeglePhoenix.Redis.command([
        "SETEX",
        "session:#{session_id}:owner_node",
        Integer.to_string(OmeglePhoenix.Config.get_session_ttl()),
        Atom.to_string(Node.self())
      ])

      :ok
    end
  end

  def unregister(session_id) when is_binary(session_id) do
    Phoenix.PubSub.unsubscribe(OmeglePhoenix.PubSub, topic(session_id))
    OmeglePhoenix.Redis.command(["DEL", "session:#{session_id}:owner_node"])
    :ok
  end

  def send_message(session_id, message) do
    route(session_id, {:router_message, message})
  end

  def notify_match(session_id, partner_session_id, common_interests \\ []) do
    route(session_id, {:router_match, partner_session_id, common_interests})
  end

  def notify_disconnect(session_id, reason) do
    Logger.info("Notifying disconnect for session: #{session_id}, reason: #{reason}")
    route(session_id, {:router_disconnect, reason})
  end

  def notify_timeout(session_id) do
    route(session_id, :router_timeout)
  end

  def notify_banned(session_id, reason) do
    route(session_id, {:router_banned, reason})
  end

  @impl true
  def init(_opts) do
    state = %{connection: nil, channel: node_channel(Atom.to_string(Node.self()))}
    send(self(), @reconnect_message)
    {:ok, state}
  end

  @impl true
  def handle_info(@reconnect_message, state) do
    stop_connection(state.connection)

    case start_subscription(state.channel) do
      {:ok, connection} ->
        {:noreply, %{state | connection: connection}}

      {:error, reason} ->
        Logger.error("Router Redis subscription failed for #{state.channel}: #{inspect(reason)}")
        Process.send_after(self(), @reconnect_message, 1_000)
        {:noreply, %{state | connection: nil}}
    end
  end

  def handle_info(
        {:redix_pubsub, _pid, _ref, :message, %{channel: channel, payload: payload}},
        state
      ) do
    handle_remote_message(channel, payload, state.channel)
    {:noreply, state}
  end

  def handle_info({:redix_pubsub, _pid, _ref, :disconnected, reason}, state) do
    Logger.warning("Router Redis pub/sub disconnected: #{inspect(reason)}")
    Process.send_after(self(), @reconnect_message, 1_000)
    {:noreply, %{state | connection: nil}}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    stop_connection(state.connection)
    :ok
  end

  defp route(session_id, message) do
    current_node = Atom.to_string(Node.self())

    case owner_node(session_id) do
      {:ok, nil} ->
        dispatch_local(session_id, message)

      {:ok, owner} when owner == current_node ->
        dispatch_local(session_id, message)

      {:ok, owner} ->
        dispatch_remote(session_id, owner, message)

      {:error, reason} ->
        Logger.warning("Router owner lookup failed for #{session_id}: #{inspect(reason)}")
        dispatch_local(session_id, message)
    end
  end

  defp owner_node(session_id) do
    OmeglePhoenix.Redis.command(["GET", "session:#{session_id}:owner_node"])
  end

  defp dispatch_local(session_id, message) do
    Logger.debug("Router dispatching locally for session: #{session_id}, message: #{inspect(message)}")
    Phoenix.PubSub.local_broadcast(
      OmeglePhoenix.PubSub,
      topic(session_id),
      message
    )
  end

  defp dispatch_remote(session_id, owner, message) do
    payload =
      session_id
      |> serialize_remote_message(message)
      |> Jason.encode!()

    case OmeglePhoenix.Redis.command(["PUBLISH", node_channel(owner), payload]) do
      {:ok, subscribers} when is_integer(subscribers) and subscribers > 0 ->
        :ok

      {:ok, 0} ->
        Logger.warning(
          "Router remote delivery found no subscribers for #{owner}; clearing stale owner for #{session_id}"
        )

        OmeglePhoenix.Redis.command(["DEL", "session:#{session_id}:owner_node"])
        dispatch_local(session_id, message)

      {:error, reason} ->
        Logger.error("Router remote delivery failed for #{owner}: #{inspect(reason)}")
        dispatch_local(session_id, message)
    end
  end

  defp serialize_remote_message(session_id, {:router_message, message}) do
    %{"session_id" => session_id, "kind" => "message", "payload" => message}
  end

  defp serialize_remote_message(session_id, {:router_match, partner_session_id, common_interests}) do
    %{
      "session_id" => session_id,
      "kind" => "match",
      "partner_session_id" => partner_session_id,
      "common_interests" => common_interests
    }
  end

  defp serialize_remote_message(session_id, {:router_disconnect, reason}) do
    %{"session_id" => session_id, "kind" => "disconnect", "reason" => reason}
  end

  defp serialize_remote_message(session_id, :router_timeout) do
    %{"session_id" => session_id, "kind" => "timeout"}
  end

  defp serialize_remote_message(session_id, {:router_banned, reason}) do
    %{"session_id" => session_id, "kind" => "banned", "reason" => reason}
  end

  defp handle_remote_message(channel, payload, expected_channel)
       when channel == expected_channel do
    with {:ok, %{"session_id" => session_id, "kind" => kind} = decoded} <- Jason.decode(payload),
         message when not is_nil(message) <- deserialize_remote_message(kind, decoded) do
      dispatch_local(session_id, message)
    else
      {:error, reason} ->
        Logger.warning("Router failed to decode remote message: #{inspect(reason)}")

      nil ->
        Logger.warning("Router received unsupported remote message payload")
    end
  end

  defp handle_remote_message(_channel, _payload, _expected_channel), do: :ok

  defp deserialize_remote_message("message", %{"payload" => payload}),
    do: {:router_message, payload}

  defp deserialize_remote_message("match", %{
         "partner_session_id" => partner_session_id,
         "common_interests" => common_interests
       }) do
    {:router_match, partner_session_id, common_interests}
  end

  defp deserialize_remote_message("disconnect", %{"reason" => reason}),
    do: {:router_disconnect, reason}

  defp deserialize_remote_message("timeout", _payload), do: :router_timeout
  defp deserialize_remote_message("banned", %{"reason" => reason}), do: {:router_banned, reason}
  defp deserialize_remote_message(_kind, _payload), do: nil

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

    with {:ok, connection} <- Redix.PubSub.start_link(opts),
         {:ok, _ref} <- Redix.PubSub.subscribe(connection, channel, self()) do
      {:ok, connection}
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

  defp node_channel(owner_node), do: "router:node:" <> owner_node
  defp topic(session_id), do: "session:" <> session_id
end
