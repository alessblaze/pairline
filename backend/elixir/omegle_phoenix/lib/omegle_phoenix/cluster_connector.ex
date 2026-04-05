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
    state = %{
      interval_ms: OmeglePhoenix.Config.get_cluster_connect_interval_ms(),
      initial_delay_ms: OmeglePhoenix.Config.get_cluster_initial_connect_delay_ms(),
      retry_attempts: OmeglePhoenix.Config.get_cluster_connect_retry_attempts(),
      retry_delay_ms: OmeglePhoenix.Config.get_cluster_connect_retry_delay_ms()
    }

    if clustering_enabled?() do
      Process.send_after(self(), @reconnect_message, state.initial_delay_ms)
    else
      Logger.info("Cluster connector disabled: CLUSTER_NODES is empty")
    end

    {:ok, state}
  end

  @impl true
  def handle_info(@reconnect_message, state) do
    connect_configured_nodes(state)
    Process.send_after(self(), @reconnect_message, state.interval_ms)
    {:noreply, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  defp connect_configured_nodes(state) do
    current_node = Node.self()

    OmeglePhoenix.Config.get_cluster_nodes()
    |> Enum.reject(&(&1 == current_node))
    |> Enum.each(fn target_node ->
      if target_node in Node.list() do
        :ok
      else
        case connect_with_retry(target_node, state.retry_attempts, state.retry_delay_ms) do
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

  defp connect_with_retry(target_node, attempts, retry_delay_ms)

  defp connect_with_retry(target_node, attempts, retry_delay_ms) when attempts > 1 do
    case Node.connect(target_node) do
      true ->
        true

      false ->
        Process.sleep(retry_delay_ms)
        connect_with_retry(target_node, attempts - 1, retry_delay_ms)

      :ignored = ignored ->
        ignored
    end
  end

  defp connect_with_retry(target_node, _attempts, _retry_delay_ms) do
    Node.connect(target_node)
  end

  defp clustering_enabled? do
    OmeglePhoenix.Config.get_cluster_nodes() != []
  end
end
