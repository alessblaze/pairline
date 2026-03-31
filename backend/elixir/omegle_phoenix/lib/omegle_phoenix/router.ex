defmodule OmeglePhoenix.Router do
  @moduledoc """
  Process registry for session-to-channel-pid routing.

  Uses a GenServer with an ETS-backed lookup table for O(1) concurrent
  reads instead of serializing all lookups through the GenServer mailbox.
  Writes (register/unregister) still go through GenServer to maintain
  process monitoring.
  """
  use GenServer
  require Logger

  @table :session_router_table

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  # ── Read API: direct ETS lookup, no GenServer bottleneck ──

  def find_process(session_id) do
    case :ets.lookup(@table, session_id) do
      [{^session_id, %{pid: pid}}] -> {:ok, pid}
      [] -> {:error, :not_found}
    end
  end

  def send_message(session_id, message) do
    case find_process(session_id) do
      {:ok, pid} ->
        send(pid, {:message, message})
        :ok

      {:error, :not_found} ->
        {:error, :not_found}
    end
  end

  def notify_match(session_id, partner_session_id) do
    case find_process(session_id) do
      {:ok, pid} ->
        send(pid, {:match, partner_session_id})
        :ok

      {:error, :not_found} ->
        {:error, :not_found}
    end
  end

  def notify_disconnect(session_id, reason) do
    case find_process(session_id) do
      {:ok, pid} ->
        send(pid, {:disconnect, reason})
        :ok

      {:error, :not_found} ->
        {:error, :not_found}
    end
  end

  # ── Write API: through GenServer for process monitoring ──

  def register(session_id, pid) do
    GenServer.call(__MODULE__, {:register, session_id, pid})
  end

  def unregister(session_id) do
    GenServer.call(__MODULE__, {:unregister, session_id})
  end

  # ── GenServer callbacks ──

  @impl true
  def init(_opts) do
    :ets.new(@table, [:named_table, :public, :set, read_concurrency: true])
    {:ok, %{monitors: %{}}}
  end

  @impl true
  def handle_call({:register, session_id, pid}, _from, state) do
    :ets.insert(
      @table,
      {session_id,
       %{
         pid: pid,
         timestamp: System.system_time(:second)
       }}
    )

    ref = Process.monitor(pid)
    new_monitors = Map.put(state.monitors, ref, session_id)
    {:reply, :ok, %{state | monitors: new_monitors}}
  end

  def handle_call({:unregister, session_id}, _from, state) do
    :ets.delete(@table, session_id)
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
  def handle_info({:DOWN, ref, :process, _pid, _info}, state) do
    case Map.pop(state.monitors, ref) do
      {nil, monitors} ->
        {:noreply, %{state | monitors: monitors}}

      {session_id, monitors} ->
        :ets.delete(@table, session_id)
        {:noreply, %{state | monitors: monitors}}
    end
  end

  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state) do
    :ok
  end
end
