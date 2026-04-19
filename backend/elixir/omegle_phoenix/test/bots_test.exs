live_redis_enabled? =
  System.get_env("LIVE_REDIS_CLUSTER_TESTS") in ["1", "true", "TRUE", "yes", "on"]

if live_redis_enabled? do
  defmodule OmeglePhoenix.BotsTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    @moduletag skip: "stubbed bot unit tests are disabled during live Redis integration runs"
  end
else
  Code.require_file("support/eredis_cluster_stub.ex", __DIR__)
  Code.require_file("support/eredis_cluster.ex", __DIR__)

  defmodule OmeglePhoenix.BotsTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    setup do
      EredisClusterStub.reset()
      :ok
    end

    test "reserve_definition_slot uses global and per-definition limits" do
      parent = self()

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster,
        [
          "EVAL",
          _script,
          "2",
          "bots:active_runs:global",
          "bots:active_runs:definition:def-1",
          "7",
          "3",
          "360"
        ] ->
          send(parent, :reserve_called)
          {:ok, 1}
      end)

      definition = %{"id" => "def-1", "bot_count" => 3, "session_ttl_seconds" => 300}
      settings = %{"max_concurrent_runs" => 7}

      assert OmeglePhoenix.Bots.reserve_definition_slot(definition, settings) == :ok
      assert_received :reserve_called
    end

    test "reserve_definition_slot reports global capacity reached" do
      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster,
        [
          "EVAL",
          _script,
          "2",
          "bots:active_runs:global",
          "bots:active_runs:definition:def-2",
          "5",
          "2",
          "180"
        ] ->
          {:ok, -2}
      end)

      definition = %{"id" => "def-2", "bot_count" => 2, "session_ttl_seconds" => 120}
      settings = %{"max_concurrent_runs" => 5}

      assert OmeglePhoenix.Bots.reserve_definition_slot(definition, settings) == :global_full
    end

    test "release_definition_slot decrements both global and definition counters" do
      parent = self()

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster,
        ["EVAL", _script, "2", "bots:active_runs:global", "bots:active_runs:definition:def-3"] ->
          send(parent, :release_called)
          {:ok, 0}
      end)

      assert OmeglePhoenix.Bots.release_definition_slot(%{"id" => "def-3"}) == :ok
      assert_received :release_called
    end
  end
end
