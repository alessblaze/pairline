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
    @router_owner_table :omegle_phoenix_router_owners

    setup do
      EredisClusterStub.reset()

      case :ets.whereis(@router_owner_table) do
        :undefined ->
          :ets.new(@router_owner_table, [:named_table, :public, :set, read_concurrency: true])

        _table ->
          :ets.delete_all_objects(@router_owner_table)
      end

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
          "bots:active_runs:{bot-active-runs}:global",
          "bots:active_runs:{bot-active-runs}:definition:def-1",
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
          "bots:active_runs:{bot-active-runs}:global",
          "bots:active_runs:{bot-active-runs}:definition:def-2",
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
        [
          "EVAL",
          _script,
          "2",
          "bots:active_runs:{bot-active-runs}:global",
          "bots:active_runs:{bot-active-runs}:definition:def-3"
        ] ->
          send(parent, :release_called)
          {:ok, 0}
      end)

      assert OmeglePhoenix.Bots.release_definition_slot(%{"id" => "def-3"}) == :ok
      assert_received :release_called
    end

    test "prioritize_definitions keeps higher bot-type priority tiers ahead of lower ones" do
      definitions = [
        %{"id" => "eng-1", "bot_type" => "engagement", "traffic_weight" => 1, "bot_count" => 1},
        %{"id" => "ai-1", "bot_type" => "ai", "traffic_weight" => 1, "bot_count" => 1}
      ]

      settings = %{"engagement_priority" => 200, "ai_priority" => 100}

      prioritized = OmeglePhoenix.Bots.prioritize_definitions(definitions, settings)

      assert Enum.map(prioritized, & &1["id"]) == ["eng-1", "ai-1"]
    end

    test "prioritize_definitions keeps all same-priority definitions and favors higher weights over repeated runs" do
      definitions = [
        %{"id" => "heavy", "bot_type" => "engagement", "traffic_weight" => 50, "bot_count" => 1},
        %{"id" => "light", "bot_type" => "engagement", "traffic_weight" => 1, "bot_count" => 1}
      ]

      settings = %{"engagement_priority" => 100, "ai_priority" => 100}

      {heavy_first, light_first} =
        Enum.reduce(1..500, {0, 0}, fn _, {heavy_acc, light_acc} ->
          [first | _rest] = OmeglePhoenix.Bots.prioritize_definitions(definitions, settings)

          case first["id"] do
            "heavy" -> {heavy_acc + 1, light_acc}
            "light" -> {heavy_acc, light_acc + 1}
          end
        end)

      assert heavy_first > light_first
      assert heavy_first + light_first == 500
    end

    test "script worker idle timeout disconnects the human via the fallback path" do
      human_session_id = "human-timeout-test"
      bot_session_id = "bot-timeout-test"
      match_generation = "gen-timeout-test"

      true = :ets.insert(@router_owner_table, {human_session_id, self(), "owner-token"})

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster,
        ["GET", "session:locator:" <> ^bot_session_id] ->
          {:error, :timeout}
      end)

      state = %{
        human_session_id: human_session_id,
        bot_session_id: bot_session_id,
        definition: %{"id" => "eng-timeout"},
        match_generation: match_generation,
        bot_messages_sent: 1,
        max_messages: 4,
        delivery_in_flight: false,
        delivery_timer: nil,
        queued_replies: [],
        idle_timer: nil,
        ttl_timer: nil
      }

      assert {:stop, :normal, ^state} =
               OmeglePhoenix.Bots.ScriptWorker.handle_info(:idle_timeout_reached, state)

      assert_receive {:router_disconnect, "bot timed out", ^match_generation}
    end
  end
end
