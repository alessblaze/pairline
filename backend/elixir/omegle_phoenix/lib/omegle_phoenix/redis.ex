# Pairline - Open Source Video Chat and Matchmaking
# Copyright (C) 2026 Albert Blasczykowski
# Aless Microsystems
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

defmodule OmeglePhoenix.Redis do
  use Supervisor
  require OpenTelemetry.Tracer, as: Tracer
  alias OmeglePhoenix.Tracing

  alias OmeglePhoenix.Redis.AdminSubscriber

  @cluster_name :omegle_phoenix_redis_cluster
  @default_timeout 60_000
  @any_route_key "__eredis_cluster_any__"
  @any_commands MapSet.new([
                  "CLUSTER",
                  "COMMAND",
                  "DBSIZE",
                  "INFO",
                  "PING",
                  "PUBLISH",
                  "PUBSUB"
                ])
  @integer_response_commands MapSet.new([
                               "DEL",
                               "DBSIZE",
                               "EXISTS",
                               "EXPIRE",
                               "PEXPIRE",
                               "PUBLISH",
                               "SADD",
                               "SCARD",
                               "SREM",
                               "XACK",
                               "ZADD",
                               "ZCARD",
                               "ZREM"
                             ])

  def start_link(opts \\ []) do
    Supervisor.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def command(cmd, opts \\ []) do
    command = normalize_command(cmd)
    command_name = command_name(command)

    Tracer.with_span "redis.command", %{kind: :client} do
      Tracing.annotate_client("redis.command")

      Tracer.set_attributes(%{
        "db.system" => "redis",
        "db.operation" => command_name,
        "redis.args_count" => max(length(command) - 1, 0)
      })

      with_timeout(opts, fn ->
        case :eredis_cluster.q(@cluster_name, command) do
          {:error, :invalid_cluster_command} ->
            :eredis_cluster.qk(@cluster_name, command, @any_route_key)

          other ->
            other
        end
      end)
      |> normalize_command_result(command_name)
      |> tap_redis_result()
    end
  end

  def pipeline(commands, opts \\ [])
  def pipeline([], _opts), do: {:ok, []}

  def pipeline(commands, opts) do
    normalized = Enum.map(commands, &normalize_command/1)

    Tracer.with_span "redis.pipeline", %{kind: :client} do
      Tracing.annotate_client("redis.pipeline")

      Tracer.set_attributes(%{
        "db.system" => "redis",
        "redis.pipeline.size" => length(normalized)
      })

      with_timeout(opts, fn -> run_pipeline(normalized) end)
      |> normalize_pipeline_result(normalized)
      |> tap_redis_result()
    end
  end

  def mget(keys, opts \\ [])

  def mget([], _opts), do: {:ok, []}

  def mget(keys, opts) when is_list(keys) do
    Tracer.with_span "redis.mget", %{kind: :client} do
      Tracing.annotate_client("redis.mget")

      Tracer.set_attributes(%{
        "db.system" => "redis",
        "db.operation" => "MGET",
        "redis.key_count" => length(keys)
      })

      keys
      |> Enum.map(&["GET", &1])
      |> pipeline(opts)
      |> tap_redis_result()
    end
  end

  def publish(channel, message, opts \\ []) do
    payload = JSON.encode!(message)

    Tracer.with_span "redis.publish", %{kind: :client} do
      Tracing.annotate_client("redis.publish")

      Tracer.set_attributes(%{
        "db.system" => "redis",
        "db.operation" => "PUBLISH",
        "messaging.destination" => channel
      })

      command(["PUBLISH", channel, payload], opts)
      |> tap_redis_result()
    end
  end

  def subscribe(_channel), do: {:error, :unsupported}
  def unsubscribe(_channel), do: {:error, :unsupported}

  @impl true
  def init(_opts) do
    with :ok <- ensure_cluster_started(),
         :ok <- connect_cluster() do
      Supervisor.init([AdminSubscriber], strategy: :one_for_one)
    else
      {:error, reason} ->
        {:stop, {:redis_cluster_connect_failed, reason}}
    end
  end

  defp ensure_cluster_started do
    case Application.ensure_all_started(:eredis_cluster) do
      {:ok, _apps} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  defp connect_cluster do
    init_nodes =
      redis_init_nodes()
      |> Enum.map(fn {host, port} -> {String.to_charlist(host), port} end)

    Application.put_env(:eredis_cluster, :pool_size, OmeglePhoenix.Config.get_redis_pool_size())

    options =
      case OmeglePhoenix.Config.get_redis_password() do
        nil -> []
        password -> [{:password, String.to_charlist(password)}]
      end

    disconnect_cluster()

    case :eredis_cluster.connect(@cluster_name, init_nodes, options) do
      :ok -> :ok
      {:error, {:already_started, _pid}} -> :ok
      {:error, reason} -> {:error, reason}
      other -> {:error, other}
    end
  end

  defp redis_init_nodes do
    case OmeglePhoenix.Config.get_redis_cluster_nodes() do
      [] -> [{OmeglePhoenix.Config.get_redis_host(), OmeglePhoenix.Config.get_redis_port()}]
      nodes -> nodes
    end
  end

  defp disconnect_cluster do
    try do
      :eredis_cluster.disconnect(@cluster_name)
    catch
      _, _ -> :ok
    end
  end

  defp tap_redis_result({:error, reason} = result) do
    Tracer.set_attribute("redis.outcome", "error")
    Tracer.set_attribute("error", true)
    Tracer.set_attribute("error.type", inspect(reason))
    result
  end

  defp tap_redis_result(result) do
    Tracer.set_attribute("redis.outcome", "ok")
    result
  end

  defp with_timeout(opts, fun) do
    timeout = Keyword.get(opts, :timeout, @default_timeout)

    timer_ref = Process.send_after(self(), {:redis_timeout, make_ref()}, timeout)

    try do
      fun.()
    catch
      :exit, {:timeout, _} -> {:error, :timeout}
    after
      Process.cancel_timer(timer_ref)
    end
  end

  defp run_pipeline(commands) do
    cond do
      Enum.all?(commands, &any_command?/1) ->
        :eredis_cluster.qk(@cluster_name, commands, @any_route_key)

      Enum.any?(commands, &any_command?/1) ->
        run_mixed_pipeline(commands)

      true ->
        :eredis_cluster.qmn(@cluster_name, commands)
    end
  end

  defp run_mixed_pipeline(commands) do
    {keyless_entries, keyed_entries} =
      commands
      |> Enum.with_index()
      |> Enum.split_with(fn {command, _index} -> any_command?(command) end)

    with {:ok, keyless_results} <- run_indexed_pipeline(keyless_entries, :keyless),
         {:ok, keyed_results} <- run_indexed_pipeline(keyed_entries, :keyed) do
      keyless_results
      |> Kernel.++(keyed_results)
      |> Enum.sort_by(&elem(&1, 0))
      |> Enum.map(&elem(&1, 1))
    end
  end

  defp run_indexed_pipeline([], _kind), do: {:ok, []}

  defp run_indexed_pipeline(entries, :keyless) do
    commands = Enum.map(entries, &elem(&1, 0))

    case :eredis_cluster.qk(@cluster_name, commands, @any_route_key) do
      {:ok, results} when is_list(results) ->
        {:ok, Enum.zip(Enum.map(entries, &elem(&1, 1)), results)}

      results when is_list(results) ->
        {:ok, Enum.zip(Enum.map(entries, &elem(&1, 1)), results)}

      {:error, reason} ->
        {:error, reason}

      other ->
        {:error, {:unexpected_pipeline_result, other}}
    end
  end

  defp run_indexed_pipeline(entries, :keyed) do
    commands = Enum.map(entries, &elem(&1, 0))

    case :eredis_cluster.qmn(@cluster_name, commands) do
      {:ok, results} when is_list(results) ->
        {:ok, Enum.zip(Enum.map(entries, &elem(&1, 1)), results)}

      results when is_list(results) ->
        {:ok, Enum.zip(Enum.map(entries, &elem(&1, 1)), results)}

      {:error, reason} ->
        {:error, reason}

      other ->
        {:error, {:unexpected_pipeline_result, other}}
    end
  end

  defp normalize_pipeline_result(results, commands) when is_list(results) and is_list(commands) do
    Enum.zip(results, commands)
    |> Enum.reduce_while({:ok, []}, fn
      {{:ok, value}, command}, {:ok, acc} ->
        {:cont, {:ok, [normalize_redis_value(command_name(command), value) | acc]}}

      {{:error, reason}, _command}, _acc ->
        {:halt, {:error, reason}}

      {other, _command}, _acc ->
        {:halt, {:error, {:unexpected_pipeline_entry, other}}}
    end)
    |> case do
      {:ok, values} -> {:ok, Enum.reverse(values)}
      error -> error
    end
  end

  defp normalize_pipeline_result({:ok, results}, commands) when is_list(results),
    do: normalize_pipeline_result(results, commands)

  defp normalize_pipeline_result({:error, reason}, _commands), do: {:error, reason}

  defp normalize_pipeline_result(other, _commands),
    do: {:error, {:unexpected_pipeline_result, other}}

  defp normalize_command_result({:ok, value}, command_name),
    do: {:ok, normalize_redis_value(command_name, value)}

  defp normalize_command_result({:error, reason}, _command_name), do: {:error, reason}
  defp normalize_command_result(other, _command_name), do: other

  defp normalize_command(command) when is_list(command) do
    Enum.map(command, &normalize_value/1)
  end

  defp command_name([command | _rest]), do: String.upcase(to_string(command))
  defp command_name(_command), do: nil

  defp any_command?(command), do: MapSet.member?(@any_commands, command_name(command))

  defp normalize_value(value) when is_binary(value), do: value
  defp normalize_value(value) when is_atom(value), do: Atom.to_string(value)
  defp normalize_value(value) when is_integer(value), do: Integer.to_string(value)

  defp normalize_value(value) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp normalize_value(value), do: to_string(value)

  defp normalize_redis_value(_command_name, :undefined), do: nil

  defp normalize_redis_value(command_name, value) when is_binary(value) do
    if MapSet.member?(@integer_response_commands, command_name) do
      case Integer.parse(value) do
        {parsed, ""} -> parsed
        _ -> value
      end
    else
      value
    end
  end

  defp normalize_redis_value(command_name, value) when is_list(value) do
    Enum.map(value, &normalize_redis_value(command_name, &1))
  end

  defp normalize_redis_value(command_name, value) when is_tuple(value) do
    value
    |> Tuple.to_list()
    |> Enum.map(&normalize_redis_value(command_name, &1))
    |> List.to_tuple()
  end

  defp normalize_redis_value(_command_name, value), do: value
end
