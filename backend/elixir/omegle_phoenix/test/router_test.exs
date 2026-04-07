live_redis_enabled? =
  System.get_env("LIVE_REDIS_CLUSTER_TESTS") in ["1", "true", "TRUE", "yes", "on"]

if live_redis_enabled? do
  defmodule OmeglePhoenix.RouterTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    @moduletag skip: "stubbed router unit tests are disabled during live Redis integration runs"
  end
else
  Code.require_file("support/eredis_cluster_stub.ex", __DIR__)
  Code.require_file("support/eredis_cluster.ex", __DIR__)

  defmodule OmeglePhoenix.RouterTest do
    use ExUnit.Case, async: false
    @moduletag capture_log: true

    setup do
      EredisClusterStub.reset()
      {:ok, _} = Application.ensure_all_started(:phoenix_pubsub)
      {:ok, redis_store} = Agent.start_link(fn -> %{} end)

      session_id = "session-router-1"
      route = %{mode: "text", shard: 0}
      locator_key = OmeglePhoenix.RedisKeys.session_locator_key(session_id)
      owner_key = OmeglePhoenix.RedisKeys.session_owner_key(session_id, route)

      Agent.update(redis_store, fn state ->
        Map.put(state, locator_key, OmeglePhoenix.RedisKeys.encode_locator(route))
      end)

      EredisClusterStub.put(:q, fn
        :omegle_phoenix_redis_cluster, ["GET", key] ->
          {:ok, Agent.get(redis_store, &Map.get(&1, key))}

        :omegle_phoenix_redis_cluster, ["SETEX", key, _ttl, value] ->
          Agent.update(redis_store, &Map.put(&1, key, value))
          {:ok, "OK"}

        :omegle_phoenix_redis_cluster, ["EVAL", _script, "1", key, expected] ->
          Agent.get_and_update(redis_store, fn state ->
            if Map.get(state, key) == expected do
              {{:ok, 1}, Map.delete(state, key)}
            else
              {{:ok, 0}, state}
            end
          end)
      end)

      EredisClusterStub.put(:qmn, fn _cluster, commands ->
        raise "unexpected qmn call in router test: #{inspect(commands)}"
      end)

      EredisClusterStub.put(:qk, fn _cluster, command, route_key ->
        raise "unexpected qk call in router test: #{inspect({command, route_key})}"
      end)

      start_supervised!({Phoenix.PubSub, name: OmeglePhoenix.PubSub})
      start_supervised!(OmeglePhoenix.Router)

      {:ok, session_id: session_id, route: route, owner_key: owner_key}
    end

    test "router refresh heals ownership after a router restart and preserves delivery", %{
      session_id: session_id,
      route: route,
      owner_key: owner_key
    } do
      assert :ok = OmeglePhoenix.Router.register(session_id, self())
      assert [{^session_id, pid, _token}] = :ets.lookup(:omegle_phoenix_router_owners, session_id)
      assert pid == self()

      old_router = Process.whereis(OmeglePhoenix.Router)
      Process.exit(old_router, :kill)

      new_router =
        wait_until(fn ->
          case Process.whereis(OmeglePhoenix.Router) do
            nil -> nil
            pid when pid != old_router -> pid
            _ -> nil
          end
        end)

      assert is_pid(new_router)
      assert [] == :ets.lookup(:omegle_phoenix_router_owners, session_id)

      OmeglePhoenix.Router.send_message(
        session_id,
        %{
          type: "message",
          from: "restart-test",
          match_generation: "gen-a",
          data: %{content: "before refresh"}
        },
        route_hint: route
      )

      assert_receive {:router_message, %{type: "message", from: "restart-test"}}, 1_000

      assert :ok = OmeglePhoenix.Router.refresh_owner(session_id, self())
      assert [{^session_id, pid, _token}] = :ets.lookup(:omegle_phoenix_router_owners, session_id)
      assert pid == self()

      assert {:ok, encoded_owner} = OmeglePhoenix.Redis.command(["GET", owner_key])
      assert is_binary(encoded_owner)

      OmeglePhoenix.Router.send_message(
        session_id,
        %{
          type: "message",
          from: "restart-test",
          match_generation: "gen-b",
          data: %{content: "after refresh"}
        }
      )

      assert_receive {:router_message, %{type: "message", from: "restart-test"}}, 1_000
    end

    test "refresh_owner does not let a client recreate the ownership ets table when router is down",
         %{
           session_id: session_id,
           owner_key: owner_key
         } do
      assert :ok = OmeglePhoenix.Router.register(session_id, self())
      assert :ok = stop_supervised(OmeglePhoenix.Router)
      assert :undefined == :ets.whereis(:omegle_phoenix_router_owners)

      assert :ok = OmeglePhoenix.Router.refresh_owner(session_id, self())
      assert :undefined == :ets.whereis(:omegle_phoenix_router_owners)

      assert {:ok, encoded_owner} = OmeglePhoenix.Redis.command(["GET", owner_key])
      assert is_binary(encoded_owner)
    end

    defp wait_until(fun, attempts \\ 40)

    defp wait_until(fun, attempts) when attempts > 0 do
      case fun.() do
        nil ->
          Process.sleep(25)
          wait_until(fun, attempts - 1)

        value ->
          value
      end
    end

    defp wait_until(_fun, 0), do: flunk("condition not met before timeout")
  end
end
