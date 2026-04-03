defmodule OmeglePhoenix.ClusterConnector do
  @moduledoc """
  Keeps a static list of BEAM nodes connected.

  This is intentionally small and dependency-free so additional Phoenix nodes
  can join the cluster just by sharing the same cookie and setting
  `CLUSTER_NODES=node1@host,node2@host`.
  """

  use GenServer
  require Logger

  @reconnect_message :connect_cluster_nodes

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    state = %{interval_ms: OmeglePhoenix.Config.get_cluster_connect_interval_ms()}

    if clustering_enabled?() do
      send(self(), @reconnect_message)
    else
      Logger.info("Cluster connector disabled: CLUSTER_NODES is empty")
    end

    {:ok, state}
  end

  @impl true
  def handle_info(@reconnect_message, state) do
    connect_configured_nodes()
    Process.send_after(self(), @reconnect_message, state.interval_ms)
    {:noreply, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  defp connect_configured_nodes do
    current_node = Node.self()

    OmeglePhoenix.Config.get_cluster_nodes()
    |> Enum.reject(&(&1 == current_node))
    |> Enum.each(fn target_node ->
      if target_node in Node.list() do
        :ok
      else
        case Node.connect(target_node) do
          true ->
            Logger.info(
              "Connected Phoenix node #{inspect(current_node)} -> #{inspect(target_node)}"
            )

          false ->
            Logger.warning(
              "Failed to connect Phoenix node #{inspect(current_node)} -> #{inspect(target_node)}"
            )

          :ignored ->
            Logger.debug("Ignored cluster connect attempt to #{inspect(target_node)}")
        end
      end
    end)
  end

  defp clustering_enabled? do
    OmeglePhoenix.Config.get_cluster_nodes() != []
  end
end
