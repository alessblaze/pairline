defmodule OmeglePhoenix.Router do
  @moduledoc """
  Cluster-aware session event routing.

  Each channel process subscribes to a PubSub topic derived from its session ID.
  Messages are then broadcast across the BEAM cluster instead of relying on
  node-local ETS process lookup.
  """

  use GenServer

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def register(session_id, pid) when is_binary(session_id) and is_pid(pid) do
    if pid != self() do
      {:error, :must_register_from_owner_process}
    else
      :ok = Phoenix.PubSub.subscribe(OmeglePhoenix.PubSub, topic(session_id))

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
    Phoenix.PubSub.broadcast(
      OmeglePhoenix.PubSub,
      topic(session_id),
      {:router_message, message}
    )
  end

  def notify_match(session_id, partner_session_id, common_interests \\ []) do
    Phoenix.PubSub.broadcast(
      OmeglePhoenix.PubSub,
      topic(session_id),
      {:router_match, partner_session_id, common_interests}
    )
  end

  def notify_disconnect(session_id, reason) do
    Phoenix.PubSub.broadcast(
      OmeglePhoenix.PubSub,
      topic(session_id),
      {:router_disconnect, reason}
    )
  end

  def notify_timeout(session_id) do
    Phoenix.PubSub.broadcast(
      OmeglePhoenix.PubSub,
      topic(session_id),
      :router_timeout
    )
  end

  def notify_banned(session_id, reason) do
    Phoenix.PubSub.broadcast(
      OmeglePhoenix.PubSub,
      topic(session_id),
      {:router_banned, reason}
    )
  end

  @impl true
  def init(_opts) do
    {:ok, %{}}
  end

  defp topic(session_id), do: "session:" <> session_id
end
