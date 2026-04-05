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
        cluster_mget(keys, opts)

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
              Supervisor.child_spec({Connection, [name: via(index)]},
                id: {:redis_connection, index}
              )
            end) ++
            [AdminSubscriber]
      end

    Supervisor.init(children, strategy: :one_for_one)
  end

  defp cluster_command(cmd, opts) do
    command = normalize_command(cmd)
    route_key = command_route_key(command)

    case RedisCluster.Cluster.command(
           cluster_config(),
           command,
           route_key,
           cluster_request_opts(opts)
         ) do
      {:error, reason} -> {:error, reason}
      result -> {:ok, result}
    end
  end

  defp cluster_pipeline(commands, opts) do
    normalized = Enum.map(commands, &normalize_command/1)

    case pipeline_route_key(normalized) do
      {:ok, :fallback} ->
        cluster_pipeline_fallback(normalized, opts)

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

  defp cluster_mget(keys, opts) do
    keys
    |> Enum.with_index()
    |> Enum.group_by(fn {key, _index} -> slot_for_key(key) end)
    |> run_cluster_groups(opts, fn entries ->
      route_key = entries |> hd() |> elem(0)
      commands = Enum.map(entries, fn {key, _index} -> ["GET", key] end)

      case RedisCluster.Cluster.pipeline(
             cluster_config(),
             commands,
             route_key,
             cluster_request_opts(opts)
           ) do
        {:error, reason} ->
          {:error, reason}

        results ->
          indexed_results =
            entries
            |> Enum.map(&elem(&1, 1))
            |> Enum.zip(results)

          {:ok, indexed_results}
      end
    end)
    |> assemble_indexed_results(length(keys))
  end

  defp cluster_pipeline_fallback(commands, opts) do
    commands
    |> Enum.with_index()
    |> Enum.group_by(fn {command, _index} ->
      case command_route_key(command) do
        :any -> :any
        route_key -> slot_for_key(route_key)
      end
    end)
    |> run_cluster_groups(opts, fn entries ->
      route_key =
        case hd(entries) |> elem(0) |> command_route_key() do
          :any -> :any
          value -> value
        end

      grouped_commands = Enum.map(entries, fn {command, _index} -> command end)

      case RedisCluster.Cluster.pipeline(
             cluster_config(),
             grouped_commands,
             route_key,
             cluster_request_opts(opts)
           ) do
        {:error, reason} ->
          {:error, reason}

        results ->
          indexed_results =
            entries
            |> Enum.map(&elem(&1, 1))
            |> Enum.zip(results)

          {:ok, indexed_results}
      end
    end)
    |> assemble_indexed_results(length(commands))
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

  defp slot_for_key(key) when is_binary(key) do
    key
    |> hash_key_for_slot()
    |> crc16_xmodem()
    |> rem(16_384)
  end

  defp hash_key_for_slot(key) do
    case Regex.run(~r/\{([^}]+)\}/, key) do
      [_, tag] when byte_size(tag) > 0 -> tag
      _ -> key
    end
  end

  defp crc16_xmodem(data), do: crc16_xmodem(data, 0)
  defp crc16_xmodem(<<>>, crc), do: crc

  defp crc16_xmodem(<<byte, rest::binary>>, crc) do
    crc = Bitwise.bxor(crc, Bitwise.bsl(byte, 8))

    crc =
      Enum.reduce(1..8, crc, fn _, acc ->
        if Bitwise.band(acc, 0x8000) != 0 do
          Bitwise.band(Bitwise.bxor(Bitwise.bsl(acc, 1), 0x1021), 0xFFFF)
        else
          Bitwise.band(Bitwise.bsl(acc, 1), 0xFFFF)
        end
      end)

    crc16_xmodem(rest, crc)
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

  defp run_cluster_groups(grouped_entries, opts, fun) do
    timeout = Keyword.get(opts, :timeout, @default_timeout)

    grouped_entries
    |> Map.values()
    |> Task.async_stream(fun,
      ordered: false,
      timeout: timeout + 1_000,
      max_concurrency: max(System.schedulers_online(), 1)
    )
    |> Enum.reduce_while({:ok, []}, fn
      {:ok, {:ok, indexed_results}}, {:ok, acc} ->
        {:cont, {:ok, [indexed_results | acc]}}

      {:ok, {:error, reason}}, _acc ->
        {:halt, {:error, reason}}

      {:exit, reason}, _acc ->
        {:halt, {:error, reason}}
    end)
    |> case do
      {:ok, indexed_results} ->
        {:ok, indexed_results |> Enum.reverse() |> List.flatten()}

      error ->
        error
    end
  end

  defp assemble_indexed_results({:error, reason}, _expected_length), do: {:error, reason}

  defp assemble_indexed_results({:ok, indexed_results}, expected_length) do
    results =
      indexed_results
      |> Enum.sort_by(&elem(&1, 0))
      |> Enum.map(&elem(&1, 1))

    if length(results) == expected_length do
      {:ok, results}
    else
      {:error, :incomplete_cluster_results}
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
