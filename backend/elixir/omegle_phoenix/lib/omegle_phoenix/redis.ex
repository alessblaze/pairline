defmodule OmeglePhoenix.Redis do
  use Supervisor

  alias OmeglePhoenix.Redis.{AdminSubscriber, AuthenticatedRedix, Connection}

  @cluster_name __MODULE__.Cluster
  @cluster_registry Module.concat(@cluster_name, Registry__)
  @cluster_pool Module.concat(@cluster_name, Pool__)
  @cluster_supervisor Module.concat(@cluster_name, Cluster__)
  @cluster_shard_discovery Module.concat(@cluster_name, ShardDiscovery__)
  @registry __MODULE__.Registry
  @default_timeout 5_000
  @any_commands MapSet.new([
                  "CLUSTER",
                  "COMMAND",
                  "DBSIZE",
                  "INFO",
                  "PING",
                  "PUBLISH",
                  "PUBSUB"
                ])

  def start_link(opts \\ []) do
    Supervisor.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def command(cmd, opts \\ []) do
    case OmeglePhoenix.Config.get_redis_mode() do
      :cluster -> cluster_command(cmd, opts)
      :standalone -> call_worker({:command, cmd}, opts)
    end
  end

  def pipeline(commands, opts \\ []) do
    case OmeglePhoenix.Config.get_redis_mode() do
      :cluster -> cluster_pipeline(commands, opts)
      :standalone -> call_worker({:pipeline, commands}, opts)
    end
  end

  def mget(keys, opts \\ [])

  def mget([], _opts), do: {:ok, []}

  def mget(keys, opts) when is_list(keys) do
    case OmeglePhoenix.Config.get_redis_mode() do
      :cluster ->
        keys
        |> Enum.map(fn key -> command(["GET", key], opts) end)
        |> gather_cluster_results()

      :standalone ->
        command(["MGET" | keys], opts)
    end
  end

  def publish(channel, message, opts \\ []) do
    payload = Jason.encode!(message)
    command(["PUBLISH", channel, payload], opts)
  end

  def subscribe(_channel), do: {:error, :unsupported}
  def unsubscribe(_channel), do: {:error, :unsupported}

  @impl true
  def init(_opts) do
    children =
      case OmeglePhoenix.Config.get_redis_mode() do
        :cluster ->
          [
            Supervisor.child_spec({RedisCluster.Cluster, cluster_config()}, id: :redis_cluster),
            AdminSubscriber
          ]

        :standalone ->
          pool_size = OmeglePhoenix.Config.get_redis_pool_size()

          [
            {Registry, keys: :unique, name: @registry}
          ] ++
            Enum.map(0..(pool_size - 1), fn index ->
              Supervisor.child_spec({Connection, [name: via(index)]}, id: {:redis_connection, index})
            end) ++
            [AdminSubscriber]
      end

    Supervisor.init(children, strategy: :one_for_one)
  end

  defp cluster_command(cmd, opts) do
    command = normalize_command(cmd)
    route_key = command_route_key(command)

    case RedisCluster.Cluster.command(cluster_config(), command, route_key, cluster_request_opts(opts)) do
      {:error, reason} -> {:error, reason}
      result -> {:ok, result}
    end
  end

  defp cluster_pipeline(commands, opts) do
    normalized = Enum.map(commands, &normalize_command/1)

    case pipeline_route_key(normalized) do
      {:ok, :fallback} ->
        normalized
        |> Enum.map(&cluster_command(&1, opts))
        |> gather_cluster_results()

      {:ok, route_key} ->
        case RedisCluster.Cluster.pipeline(
               cluster_config(),
               normalized,
               route_key,
               cluster_request_opts(opts)
             ) do
          {:error, reason} -> {:error, reason}
          result -> {:ok, result}
        end
    end
  end

  defp cluster_config do
    {host, port} =
      case OmeglePhoenix.Config.get_redis_cluster_nodes() do
        [{seed_host, seed_port} | _rest] ->
          {seed_host, seed_port}

        [] ->
          {OmeglePhoenix.Config.get_redis_host(), OmeglePhoenix.Config.get_redis_port()}
      end

    %RedisCluster.Configuration{
      host: host,
      port: port,
      pool_size: OmeglePhoenix.Config.get_redis_pool_size(),
      name: @cluster_name,
      registry: @cluster_registry,
      pool: @cluster_pool,
      cluster: @cluster_supervisor,
      shard_discovery: @cluster_shard_discovery,
      redis_module: AuthenticatedRedix
    }
  end

  defp cluster_request_opts(opts) do
    timeout = Keyword.get(opts, :timeout, @default_timeout)
    [timeout: timeout, compute_hash_tag: true]
  end

  defp pipeline_route_key([]), do: {:ok, :any}

  defp pipeline_route_key(commands) do
    route_keys =
      commands
      |> Enum.map(&command_route_key/1)
      |> Enum.reject(&(&1 == :any))
      |> Enum.uniq()

    case route_keys do
      [] ->
        {:ok, :any}

      [route_key] ->
        {:ok, route_key}

      keys ->
        if same_hash_tag?(keys) do
          {:ok, hd(keys)}
        else
          {:ok, :fallback}
        end
    end
  end

  defp same_hash_tag?(keys) do
    tags =
      keys
      |> Enum.map(&hash_tag/1)
      |> Enum.uniq()

    length(tags) == 1 and hd(tags) != nil
  end

  defp hash_tag(key) do
    case Regex.run(~r/\{([^}]+)\}/, key) do
      [_, tag] -> tag
      _ -> nil
    end
  end

  defp command_route_key([]), do: :any

  defp command_route_key([command | rest]) do
    cmd = String.upcase(to_string(command))

    cond do
      MapSet.member?(@any_commands, cmd) ->
        :any

      cmd == "EVAL" ->
        eval_route_key(rest)

      cmd == "EVALSHA" ->
        eval_route_key(rest)

      cmd == "XREAD" ->
        stream_route_key(rest)

      cmd == "XREADGROUP" ->
        stream_route_key(rest)

      cmd == "XINFO" ->
        list_key_at(rest, 1)

      cmd == "XGROUP" ->
        list_key_at(rest, 1)

      true ->
        list_key_at(rest, 0)
    end
  end

  defp eval_route_key([_script, numkeys | tail]) do
    case Integer.parse(to_string(numkeys)) do
      {count, ""} when count > 0 -> list_key_at(tail, 0)
      _ -> :any
    end
  end

  defp eval_route_key(_), do: :any

  defp stream_route_key(args) do
    case Enum.find_index(args, &(to_string(&1) == "STREAMS")) do
      nil -> :any
      index -> list_key_at(args, index + 1)
    end
  end

  defp list_key_at(list, index) do
    case Enum.at(list, index) do
      nil -> :any
      value -> to_string(value)
    end
  end

  defp normalize_command(command) when is_list(command) do
    Enum.map(command, &normalize_value/1)
  end

  defp normalize_value(value) when is_binary(value), do: value
  defp normalize_value(value) when is_atom(value), do: Atom.to_string(value)
  defp normalize_value(value) when is_integer(value), do: Integer.to_string(value)

  defp normalize_value(value) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp normalize_value(value), do: to_string(value)

  defp gather_cluster_results(results) do
    Enum.reduce_while(results, {:ok, []}, fn
      {:ok, value}, {:ok, acc} ->
        {:cont, {:ok, [value | acc]}}

      {:error, reason}, _acc ->
        {:halt, {:error, reason}}
    end)
    |> case do
      {:ok, values} -> {:ok, Enum.reverse(values)}
      error -> error
    end
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
