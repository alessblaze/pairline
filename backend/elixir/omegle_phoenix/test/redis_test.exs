live_redis_enabled? = System.get_env("LIVE_REDIS_CLUSTER_TESTS") in ["1", "true", "TRUE", "yes", "on"]

if live_redis_enabled? do
  defmodule OmeglePhoenix.RedisTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    @moduletag skip: "stubbed Redis unit tests are disabled during live Redis integration runs"
  end
else
  Code.require_file("support/eredis_cluster_stub.ex", __DIR__)
  Code.require_file("support/eredis_cluster.ex", __DIR__)

  defmodule OmeglePhoenix.RedisTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    setup do
      EredisClusterStub.reset()
      :ok
    end

    test "command normalizes undefined replies to nil" do
      EredisClusterStub.put(:q, fn :omegle_phoenix_redis_cluster, ["GET", "ban:ip:127.0.0.1"] ->
        {:ok, :undefined}
      end)

      assert OmeglePhoenix.Redis.command(["GET", "ban:ip:127.0.0.1"]) == {:ok, nil}
    end

    test "command falls back to qk for keyless commands rejected by q" do
      parent = self()

      EredisClusterStub.put(:q, fn :omegle_phoenix_redis_cluster, ["PING"] ->
        send(parent, :q_called)
        {:error, :invalid_cluster_command}
      end)

      EredisClusterStub.put(:qk, fn :omegle_phoenix_redis_cluster, ["PING"], route_key ->
        send(parent, {:qk_called, route_key})
        {:ok, "PONG"}
      end)

      assert OmeglePhoenix.Redis.command(["PING"]) == {:ok, "PONG"}
      assert_received :q_called
      assert_received {:qk_called, "__eredis_cluster_any__"}
    end

    test "pipeline normalizes integer responses by command" do
      EredisClusterStub.put(:qmn, fn :omegle_phoenix_redis_cluster, commands ->
        assert commands == [["EXISTS", "session:1"], ["ZCARD", "queue:1"]]
        [{:ok, "1"}, {:ok, "0"}]
      end)

      assert OmeglePhoenix.Redis.pipeline([["EXISTS", "session:1"], ["ZCARD", "queue:1"]]) ==
               {:ok, [1, 0]}
    end

    test "pipeline with only keyless commands uses qk" do
      parent = self()

      EredisClusterStub.put(:qk, fn :omegle_phoenix_redis_cluster, commands, route_key ->
        send(parent, {:qk_commands, commands, route_key})
        [{:ok, "PONG"}, {:ok, "redis_version:7.0"}]
      end)

      assert OmeglePhoenix.Redis.pipeline([["PING"], ["INFO"]]) ==
               {:ok, ["PONG", "redis_version:7.0"]}

      assert_received {:qk_commands, [["PING"], ["INFO"]], "__eredis_cluster_any__"}
    end

    test "pipeline with mixed keyless and keyed commands splits execution safely" do
      parent = self()

      EredisClusterStub.put(:qk, fn :omegle_phoenix_redis_cluster, commands, route_key ->
        send(parent, {:keyless_pipeline, commands, route_key})
        [{:ok, "PONG"}]
      end)

      EredisClusterStub.put(:qmn, fn :omegle_phoenix_redis_cluster, commands ->
        send(parent, {:keyed_pipeline, commands})
        [{:ok, :undefined}]
      end)

      assert OmeglePhoenix.Redis.pipeline([["PING"], ["GET", "foo"]]) == {:ok, ["PONG", nil]}

      assert_received {:keyless_pipeline, [["PING"]], "__eredis_cluster_any__"}
      assert_received {:keyed_pipeline, [["GET", "foo"]]}
    end
  end
end
