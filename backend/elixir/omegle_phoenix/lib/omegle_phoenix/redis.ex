defmodule OmeglePhoenix.Redis do
  use Supervisor

  alias OmeglePhoenix.Redis.{AdminSubscriber, Connection}

  @registry __MODULE__.Registry
  @default_timeout 5_000

  def start_link(opts \\ []) do
    Supervisor.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def command(cmd, opts \\ []) do
    call_worker({:command, cmd}, opts)
  end

  def pipeline(commands, opts \\ []) do
    call_worker({:pipeline, commands}, opts)
  end

  def publish(channel, message, opts \\ []) do
    payload = Jason.encode!(message)
    command(["PUBLISH", channel, payload], opts)
  end

  def subscribe(_channel), do: {:error, :unsupported}
  def unsubscribe(_channel), do: {:error, :unsupported}

  @impl true
  def init(_opts) do
    pool_size = OmeglePhoenix.Config.get_redis_pool_size()

    children =
      [
        {Registry, keys: :unique, name: @registry}
      ] ++
        Enum.map(0..(pool_size - 1), fn index ->
          Supervisor.child_spec({Connection, [name: via(index)]}, id: {:redis_connection, index})
        end) ++
        [AdminSubscriber]

    Supervisor.init(children, strategy: :one_for_one)
  end

  defp call_worker(message, opts) do
    timeout = Keyword.get(opts, :timeout, @default_timeout)
    worker = pick_worker()
    GenServer.call(worker, message, timeout)
  end

  defp pick_worker do
    pool_size = OmeglePhoenix.Config.get_redis_pool_size()
    index = :erlang.phash2({self(), System.unique_integer([:positive, :monotonic])}, pool_size)
    via(index)
  end

  defp via(index), do: {:via, Registry, {@registry, {:conn, index}}}
end
