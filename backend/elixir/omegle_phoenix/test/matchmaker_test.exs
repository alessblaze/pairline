live_redis_enabled? =
  System.get_env("LIVE_REDIS_CLUSTER_TESTS") in ["1", "true", "TRUE", "yes", "on"]

if live_redis_enabled? do
  defmodule OmeglePhoenix.MatchmakerTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    @moduletag skip:
                 "stubbed matchmaker unit tests are disabled during live Redis integration runs"
  end
else
  Code.require_file("support/eredis_cluster_stub.ex", __DIR__)
  Code.require_file("support/eredis_cluster.ex", __DIR__)

  defmodule OmeglePhoenix.MatchmakerTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    setup do
      EredisClusterStub.reset()

      original_env = %{
        "MATCH_EVENT_STREAM_BLOCK_MS" => System.get_env("MATCH_EVENT_STREAM_BLOCK_MS"),
        "MATCH_SWEEP_INTERVAL_MS" => System.get_env("MATCH_SWEEP_INTERVAL_MS"),
        "MATCH_SHARD_COUNT" => System.get_env("MATCH_SHARD_COUNT"),
        "SHARED_SECRET" => System.get_env("SHARED_SECRET")
      }

      System.put_env("MATCH_EVENT_STREAM_BLOCK_MS", "50")
      System.put_env("MATCH_SWEEP_INTERVAL_MS", "0")
      System.put_env("MATCH_SHARD_COUNT", "1")
      System.put_env("SHARED_SECRET", "test-shared")

      on_exit(fn ->
        Enum.each(original_env, fn
          {key, nil} -> System.delete_env(key)
          {key, value} -> System.put_env(key, value)
        end)
      end)

      :ok
    end

    test "stream consumption stays responsive while the blocking redis read is still waiting" do
      parent = self()

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["XGROUP", "CREATE", _stream, _group, "$", "MKSTREAM"] ->
          {:ok, "OK"}

        :omegle_phoenix_redis_cluster,
        ["XAUTOCLAIM", _stream, _group, _consumer, "30000", _start_id, "COUNT", "100"] ->
          {:ok, ["0-0", []]}

        :omegle_phoenix_redis_cluster, ["XINFO", "CONSUMERS", _stream, _group] ->
          {:ok, []}

        :omegle_phoenix_redis_cluster,
        [
          "XREADGROUP",
          "GROUP",
          _group,
          _consumer,
          "COUNT",
          _count,
          "BLOCK",
          _block_ms,
          "STREAMS",
          _stream,
          "0"
        ] ->
          {:ok, nil}

        :omegle_phoenix_redis_cluster,
        [
          "XREADGROUP",
          "GROUP",
          _group,
          _consumer,
          "COUNT",
          _count,
          "BLOCK",
          _block_ms,
          "STREAMS",
          _stream,
          ">"
        ] ->
          send(parent, {:stream_read_blocked, self()})

          receive do
            :release_stream_read -> {:ok, nil}
          after
            1_000 -> {:ok, nil}
          end

        :omegle_phoenix_redis_cluster, ["SMEMBERS", _key] ->
          {:ok, []}
      end)

      EredisClusterStub.put(:qmn, fn _cluster, commands ->
        raise "unexpected qmn call in stream responsiveness test: #{inspect(commands)}"
      end)

      EredisClusterStub.put(:qk, fn _cluster, command, route_key ->
        raise "unexpected qk call in stream responsiveness test: #{inspect({command, route_key})}"
      end)

      start_supervised!({Task.Supervisor, name: OmeglePhoenix.TaskSupervisor})
      start_supervised!(OmeglePhoenix.Matchmaker)

      assert_receive {:stream_read_blocked, blocker}, 1_000

      GenServer.cast(OmeglePhoenix.Matchmaker, {:track_fallback_generation, "session-1", 7})

      assert_eventually(fn ->
        :sys.get_state(OmeglePhoenix.Matchmaker).fallback_generations["session-1"] == 7
      end)

      send(blocker, :release_stream_read)
    end

    test "queue sweeping stays responsive while queue discovery is blocked" do
      parent = self()

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["XGROUP", "CREATE", _stream, _group, "$", "MKSTREAM"] ->
          {:ok, "OK"}

        :omegle_phoenix_redis_cluster,
        ["XAUTOCLAIM", _stream, _group, _consumer, "30000", _start_id, "COUNT", "100"] ->
          {:ok, ["0-0", []]}

        :omegle_phoenix_redis_cluster, ["XINFO", "CONSUMERS", _stream, _group] ->
          {:ok, []}

        :omegle_phoenix_redis_cluster,
        [
          "XREADGROUP",
          "GROUP",
          _group,
          _consumer,
          "COUNT",
          _count,
          "BLOCK",
          _block_ms,
          "STREAMS",
          _stream,
          _stream_id
        ] ->
          {:ok, nil}

        :omegle_phoenix_redis_cluster, ["SMEMBERS", registry_key] ->
          send(parent, {:sweep_registry_blocked, self(), registry_key})

          receive do
            :release_sweep_registry -> {:ok, []}
          after
            1_000 -> {:ok, []}
          end
      end)

      EredisClusterStub.put(:qmn, fn _cluster, commands ->
        raise "unexpected qmn call in sweep responsiveness test: #{inspect(commands)}"
      end)

      EredisClusterStub.put(:qk, fn _cluster, command, route_key ->
        raise "unexpected qk call in sweep responsiveness test: #{inspect({command, route_key})}"
      end)

      start_supervised!({Task.Supervisor, name: OmeglePhoenix.TaskSupervisor})
      start_supervised!(OmeglePhoenix.Matchmaker)

      send(OmeglePhoenix.Matchmaker, :sweep_match_queues)

      assert_receive {:sweep_registry_blocked, blocker, _registry_key}, 1_000

      GenServer.cast(OmeglePhoenix.Matchmaker, {:track_fallback_generation, "session-2", 9})

      assert_eventually(fn ->
        :sys.get_state(OmeglePhoenix.Matchmaker).fallback_generations["session-2"] == 9
      end)

      send(blocker, :release_sweep_registry)
    end

    defp assert_eventually(fun, attempts \\ 20)

    defp assert_eventually(fun, attempts) when attempts > 0 do
      if fun.() do
        :ok
      else
        Process.sleep(25)
        assert_eventually(fun, attempts - 1)
      end
    end

    defp assert_eventually(_fun, 0), do: flunk("condition not met before timeout")
  end
end
