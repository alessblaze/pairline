defmodule OmeglePhoenix.Redis.Connection do
  use GenServer
  require Logger

  defstruct [:conn]

  def start_link(opts) do
    name = Keyword.fetch!(opts, :name)
    GenServer.start_link(__MODULE__, opts, name: name)
  end

  @impl true
  def init(_opts) do
    {:ok, %__MODULE__{conn: connect()}}
  end

  @impl true
  def handle_call({:command, cmd}, _from, state) do
    {result, conn} =
      exec_with_reconnect(state.conn, fn active_conn -> Redix.command(active_conn, cmd) end)

    {:reply, result, %{state | conn: conn}}
  end

  def handle_call({:pipeline, commands}, _from, state) do
    {result, conn} =
      exec_with_reconnect(state.conn, fn active_conn -> Redix.pipeline(active_conn, commands) end)

    {:reply, result, %{state | conn: conn}}
  end

  @impl true
  def terminate(_reason, state) do
    stop_conn(state.conn)
    :ok
  end

  defp exec_with_reconnect(conn, callback) do
    case callback.(conn) do
      {:error, %Redix.ConnectionError{} = error} ->
        Logger.warning(
          "Redis connection error #{inspect(error.reason)}; reconnecting pooled connection"
        )

        stop_conn(conn)
        new_conn = connect()
        {callback.(new_conn), new_conn}

      result ->
        {result, conn}
    end
  end

  defp connect do
    opts = [
      host: OmeglePhoenix.Config.get_redis_host(),
      port: OmeglePhoenix.Config.get_redis_port()
    ]

    opts =
      case OmeglePhoenix.Config.get_redis_password() do
        nil -> opts
        password -> Keyword.put(opts, :password, password)
      end

    case Redix.start_link(opts) do
      {:ok, pid} -> pid
      {:error, reason} -> raise "Redis connection failed: #{inspect(reason)}"
    end
  end

  defp stop_conn(nil), do: :ok

  defp stop_conn(conn) do
    try do
      Redix.stop(conn)
    rescue
      _ -> :ok
    end
  end
end
