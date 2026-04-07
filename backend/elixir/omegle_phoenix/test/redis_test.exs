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

live_redis_enabled? =
  System.get_env("LIVE_REDIS_CLUSTER_TESTS") in ["1", "true", "TRUE", "yes", "on"]

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

    test "get_session propagates redis lookup errors instead of treating them as not found" do
      session_id = "session-1"
      route = %{mode: "text", shard: 0}
      locator = OmeglePhoenix.RedisKeys.encode_locator(route)
      locator_key = OmeglePhoenix.RedisKeys.session_locator_key(session_id)
      session_key = OmeglePhoenix.RedisKeys.session_key(session_id, route)

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["GET", key] when key == locator_key ->
          {:ok, locator}

        :omegle_phoenix_redis_cluster, ["GET", key] when key == session_key ->
          {:error, :closed}
      end)

      assert OmeglePhoenix.SessionManager.get_session(session_id) == {:error, :closed}
    end

    test "get_sessions propagates batched redis lookup errors instead of returning an empty map" do
      session_ids = ["session-1", "session-2"]
      route_1 = %{mode: "text", shard: 0}
      route_2 = %{mode: "text", shard: 0}

      locator_commands =
        Enum.map(session_ids, fn session_id ->
          ["GET", OmeglePhoenix.RedisKeys.session_locator_key(session_id)]
        end)

      session_commands = [
        ["GET", OmeglePhoenix.RedisKeys.session_key("session-1", route_1)],
        ["GET", OmeglePhoenix.RedisKeys.session_key("session-2", route_2)]
      ]

      EredisClusterStub.put(:qmn, fn
        :omegle_phoenix_redis_cluster, commands when commands == locator_commands ->
          [
            {:ok, OmeglePhoenix.RedisKeys.encode_locator(route_1)},
            {:ok, OmeglePhoenix.RedisKeys.encode_locator(route_2)}
          ]

        :omegle_phoenix_redis_cluster, commands when commands == session_commands ->
          {:error, :timeout}
      end)

      assert OmeglePhoenix.SessionManager.get_sessions(session_ids) == {:error, :timeout}
    end

    test "ip_ban_reason propagates redis errors instead of treating the ip as unbanned" do
      ip = "203.0.113.10"
      key = "ban:ip:#{ip}"

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["GET", lookup_key] when lookup_key == key ->
          {:error, :timeout}
      end)

      assert OmeglePhoenix.SessionManager.ip_ban_reason(ip) == {:error, :timeout}
    end

    test "count_active_sessions propagates redis errors instead of reporting zero" do
      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["SCARD", _key] ->
          {:error, :closed}
      end)

      assert OmeglePhoenix.SessionManager.count_active_sessions() == {:error, :closed}
    end

    test "get_queue_ready_sessions propagates batched redis lookup errors instead of returning an empty map" do
      session_ids = ["session-1", "session-2"]
      route = %{mode: "text", shard: 0}

      locator_commands =
        Enum.map(session_ids, fn session_id ->
          ["GET", OmeglePhoenix.RedisKeys.session_locator_key(session_id)]
        end)

      queue_meta_commands =
        Enum.map(session_ids, fn session_id ->
          ["GET", OmeglePhoenix.RedisKeys.queue_meta_key(session_id, route)]
        end)

      EredisClusterStub.put(:qmn, fn
        :omegle_phoenix_redis_cluster, commands when commands == locator_commands ->
          [
            {:ok, OmeglePhoenix.RedisKeys.encode_locator(route)},
            {:ok, OmeglePhoenix.RedisKeys.encode_locator(route)}
          ]

        :omegle_phoenix_redis_cluster, commands when commands == queue_meta_commands ->
          [{:error, :timeout}, {:error, :timeout}]
      end)

      assert OmeglePhoenix.SessionManager.get_queue_ready_sessions(session_ids) ==
               {:error, :timeout}
    end

    test "emergency_ban_ip propagates session lookup errors instead of crashing" do
      ip = "203.0.113.25"
      key = "ip:sessions:#{ip}"

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["SMEMBERS", lookup_key] when lookup_key == key ->
          {:error, :timeout}
      end)

      assert OmeglePhoenix.SessionManager.emergency_ban_ip(ip, "test ban") == {:error, :timeout}
    end

    test "emergency_unban_ip propagates session lookup errors instead of crashing" do
      ip = "203.0.113.26"
      key = "ip:sessions:#{ip}"

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["SMEMBERS", lookup_key] when lookup_key == key ->
          {:error, :closed}
      end)

      assert OmeglePhoenix.SessionManager.emergency_unban_ip(ip) == {:error, :closed}
    end
  end
end
