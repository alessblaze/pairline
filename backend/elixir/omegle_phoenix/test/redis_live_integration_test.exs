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

defmodule OmeglePhoenix.RedisLiveIntegrationTest do
  use ExUnit.Case, async: false
  @moduletag capture_log: true

  @live_flag System.get_env("LIVE_REDIS_CLUSTER_TESTS")
  @live_enabled @live_flag in ["1", "true", "TRUE", "yes", "on"]
  @required_env ~w(REDIS_CLUSTER_NODES REDIS_PASSWORD)

  if not @live_enabled do
    @moduletag skip:
                 "Set LIVE_REDIS_CLUSTER_TESTS=1 and provide REDIS_CLUSTER_NODES/REDIS_PASSWORD to run live Redis integration tests"
  end

  setup_all do
    ensure_live_env!()
    ensure_distributed_test_node!()
    Application.ensure_all_started(:omegle_phoenix)
    ensure_cluster_connected!()
    :ok
  end

  setup do
    session_id = UUID.uuid4()
    peer_session_id = UUID.uuid4()
    suffix = System.unique_integer([:positive])
    ip = "203.0.113.#{rem(suffix, 200) + 1}"
    peer_ip = "203.0.113.#{rem(suffix + 1, 200) + 1}"
    key_prefix = "test:redis_live:#{session_id}"

    on_exit(fn ->
      cleanup_session(session_id)
      cleanup_session(peer_session_id)
      cleanup_ip_ban(ip)
      cleanup_ip_ban(peer_ip)

      _ =
        OmeglePhoenix.Redis.pipeline([
          ["DEL", "#{key_prefix}:missing"],
          ["DEL", "#{key_prefix}:counter"],
          ["DEL", "#{key_prefix}:set"]
        ])
    end)

    {:ok,
     %{
       session_id: session_id,
       peer_session_id: peer_session_id,
       ip: ip,
       peer_ip: peer_ip,
       key_prefix: key_prefix
     }}
  end

  test "wrapper normalizes live redis replies", %{key_prefix: key_prefix} do
    missing_key = "#{key_prefix}:missing"
    counter_key = "#{key_prefix}:counter"
    set_key = "#{key_prefix}:set"

    assert OmeglePhoenix.Redis.pipeline([
             ["DEL", missing_key],
             ["DEL", counter_key],
             ["DEL", set_key]
           ]) ==
             {:ok, [0, 0, 0]}

    assert OmeglePhoenix.Redis.command(["GET", missing_key]) == {:ok, nil}

    assert OmeglePhoenix.Redis.command(["SADD", set_key, "alpha"]) == {:ok, 1}
    assert OmeglePhoenix.Redis.command(["EXISTS", set_key]) == {:ok, 1}
    assert OmeglePhoenix.Redis.command(["SCARD", set_key]) == {:ok, 1}

    assert OmeglePhoenix.Redis.pipeline([["PING"], ["GET", missing_key]]) == {:ok, ["PONG", nil]}
  end

  test "session manager create get and delete works end to end", %{session_id: session_id, ip: ip} do
    preferences = %{"mode" => "text", "interests" => "music,games"}

    assert {:ok, created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert created.id == session_id
    assert created.ip == ip

    assert {:ok, fetched} = OmeglePhoenix.SessionManager.get_session(session_id)
    assert fetched.id == session_id
    assert fetched.preferences["mode"] == "text"

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)

    assert OmeglePhoenix.Redis.command([
             "EXISTS",
             OmeglePhoenix.RedisKeys.session_key(session_id, route)
           ]) ==
             {:ok, 1}

    assert :ok = OmeglePhoenix.SessionManager.delete_session(session_id)

    assert_eventually(fn ->
      OmeglePhoenix.SessionManager.get_session(session_id) == {:error, :not_found}
    end)
  end

  test "count_active_sessions uses the active session index cardinality", %{
    session_id: session_id,
    peer_session_id: peer_session_id,
    ip: ip,
    peer_ip: peer_ip
  } do
    assert {:ok, baseline} = OmeglePhoenix.SessionManager.count_active_sessions()

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, %{"mode" => "text"})

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(peer_session_id, peer_ip, %{
               "mode" => "video"
             })

    assert_eventually(fn ->
      case OmeglePhoenix.SessionManager.count_active_sessions() do
        {:ok, count} -> count >= baseline + 2
        _ -> false
      end
    end)

    assert :ok = OmeglePhoenix.SessionManager.delete_session(session_id)
    assert :ok = OmeglePhoenix.SessionManager.delete_session(peer_session_id)

    assert_eventually(fn ->
      OmeglePhoenix.SessionManager.count_active_sessions() == {:ok, baseline}
    end)
  end

  test "update_session applies hot field updates without losing queue metadata", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "video", "interests" => "music,games"}

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)

    assert {:ok, updated} =
             OmeglePhoenix.SessionManager.update_session(session_id, %{
               status: :disconnecting,
               signaling_ready: true,
               webrtc_started: true
             })

    assert updated.status == :disconnecting
    assert updated.signaling_ready == true
    assert updated.webrtc_started == true

    queue_meta_key = OmeglePhoenix.RedisKeys.queue_meta_key(session_id, route)
    assert {:ok, queue_meta_payload} = OmeglePhoenix.Redis.command(["GET", queue_meta_key])
    assert {:ok, queue_meta} = Jason.decode(queue_meta_payload)
    assert queue_meta["status"] == "disconnecting"
    assert queue_meta["mode"] == "video"
    assert queue_meta["interest_buckets"] != []
  end

  test "update_session recreates queue metadata when it is missing", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "video", "interests" => "music,games"}

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)
    queue_meta_key = OmeglePhoenix.RedisKeys.queue_meta_key(session_id, route)

    assert {:ok, 1} = OmeglePhoenix.Redis.command(["DEL", queue_meta_key])

    assert {:ok, updated} =
             OmeglePhoenix.SessionManager.update_session(session_id, %{
               signaling_ready: true,
               webrtc_started: true
             })

    assert updated.signaling_ready == true
    assert updated.webrtc_started == true

    assert {:ok, queue_meta_payload} = OmeglePhoenix.Redis.command(["GET", queue_meta_key])
    assert {:ok, queue_meta} = Jason.decode(queue_meta_payload)
    assert queue_meta["mode"] == "video"
    assert queue_meta["interest_buckets"] != []
  end

  test "refresh_session recreates active locators for long-lived report lookups", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "video", "interests" => "long,report"}

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)
    locator_key = OmeglePhoenix.RedisKeys.session_locator_key(session_id)
    ip_locator_key = OmeglePhoenix.RedisKeys.session_ip_locator_key(session_id)

    Process.sleep(2_000)

    assert {:ok, locator_ttl_before} = OmeglePhoenix.Redis.command(["TTL", locator_key])
    assert {:ok, ip_locator_ttl_before} = OmeglePhoenix.Redis.command(["TTL", ip_locator_key])

    assert {:ok, [1, 1]} =
             OmeglePhoenix.Redis.pipeline([
               ["DEL", locator_key],
               ["DEL", ip_locator_key]
             ])

    assert OmeglePhoenix.Redis.command(["GET", locator_key]) == {:ok, nil}
    assert OmeglePhoenix.Redis.command(["GET", ip_locator_key]) == {:ok, nil}

    assert {:ok, _session_id} = OmeglePhoenix.SessionManager.refresh_session(session_id)

    assert OmeglePhoenix.Redis.command(["GET", locator_key]) ==
             {:ok, OmeglePhoenix.RedisKeys.encode_locator(route)}

    assert OmeglePhoenix.Redis.command(["GET", ip_locator_key]) == {:ok, ip}

    assert {:ok, locator_ttl_after} = OmeglePhoenix.Redis.command(["TTL", locator_key])
    assert {:ok, ip_locator_ttl_after} = OmeglePhoenix.Redis.command(["TTL", ip_locator_key])

    assert locator_ttl_after > locator_ttl_before
    assert ip_locator_ttl_after > ip_locator_ttl_before
  end

  test "delete_session preserves report-grace locators and session token", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "text", "interests" => "report,grace"}

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)

    assert :ok = OmeglePhoenix.SessionManager.delete_session(session_id)

    encoded_route = OmeglePhoenix.RedisKeys.encode_locator(route)
    report_locator_key = OmeglePhoenix.RedisKeys.session_report_locator_key(session_id)
    ip_locator_key = OmeglePhoenix.RedisKeys.session_ip_locator_key(session_id)
    token_key = OmeglePhoenix.RedisKeys.session_token_key(session_id, route)

    assert OmeglePhoenix.Redis.command([
             "GET",
             OmeglePhoenix.RedisKeys.session_locator_key(session_id)
           ]) ==
             {:ok, nil}

    assert OmeglePhoenix.Redis.command(["GET", report_locator_key]) == {:ok, encoded_route}
    assert OmeglePhoenix.Redis.command(["GET", ip_locator_key]) == {:ok, ip}

    assert {:ok, report_ttl} = OmeglePhoenix.Redis.command(["TTL", report_locator_key])
    assert {:ok, ip_ttl} = OmeglePhoenix.Redis.command(["TTL", ip_locator_key])
    assert {:ok, token_ttl} = OmeglePhoenix.Redis.command(["TTL", token_key])

    assert report_ttl > 0
    assert ip_ttl > 0
    assert token_ttl > 0
  end

  test "stale get_session after delete does not clear report locators", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "text", "interests" => "stale,read"}

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)
    assert :ok = OmeglePhoenix.SessionManager.delete_session(session_id)

    assert OmeglePhoenix.SessionManager.get_session(session_id) == {:error, :not_found}

    assert OmeglePhoenix.Redis.command([
             "GET",
             OmeglePhoenix.RedisKeys.session_report_locator_key(session_id)
           ]) == {:ok, OmeglePhoenix.RedisKeys.encode_locator(route)}

    assert OmeglePhoenix.Redis.command([
             "GET",
             OmeglePhoenix.RedisKeys.session_ip_locator_key(session_id)
           ]) == {:ok, ip}
  end

  test "cleanup_orphaned_session removes preserved report locators", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "text", "interests" => "cleanup,report"}

    assert {:ok, _created} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert :ok = OmeglePhoenix.SessionManager.delete_session(session_id)

    assert {:ok, _} =
             OmeglePhoenix.Redis.command([
               "GET",
               OmeglePhoenix.RedisKeys.session_report_locator_key(session_id)
             ])

    assert :ok = OmeglePhoenix.SessionManager.cleanup_orphaned_session(session_id)

    assert_eventually(fn ->
      OmeglePhoenix.Redis.command([
        "GET",
        OmeglePhoenix.RedisKeys.session_report_locator_key(session_id)
      ]) == {:ok, nil}
    end)

    assert_eventually(fn ->
      OmeglePhoenix.Redis.command([
        "GET",
        OmeglePhoenix.RedisKeys.session_ip_locator_key(session_id)
      ]) == {:ok, nil}
    end)
  end

  test "matchmaker join and leave queue works against live cluster", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "text", "interests" => "elixir,redis"}

    assert {:ok, _session} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert :ok = OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)
    membership_key = OmeglePhoenix.RedisKeys.session_queue_key(session_id, route)

    assert_eventually(fn ->
      case OmeglePhoenix.Redis.command(["SMEMBERS", membership_key]) do
        {:ok, queue_keys} when is_list(queue_keys) -> queue_keys != []
        _ -> false
      end
    end)

    assert :ok = OmeglePhoenix.Matchmaker.leave_queue(session_id)

    assert_eventually(fn ->
      OmeglePhoenix.Redis.command(["SMEMBERS", membership_key]) == {:ok, []}
    end)
  end

  test "automatic matchmaking pairs queued users and clears queue membership", %{
    session_id: session_id,
    peer_session_id: peer_session_id,
    ip: ip,
    peer_ip: peer_ip
  } do
    preferences = %{"mode" => "text", "interests" => "auto,match"}

    assert {:ok, _session_1} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, _session_2} =
             OmeglePhoenix.SessionManager.create_session(peer_session_id, peer_ip, preferences)

    assert {:ok, session_1} = OmeglePhoenix.SessionManager.get_session(session_id)

    assert {:ok, _moved_session_2} =
             OmeglePhoenix.SessionManager.move_session_shard(
               peer_session_id,
               session_1.redis_shard
             )

    assert :ok = OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)
    assert :ok = OmeglePhoenix.Matchmaker.join_queue(peer_session_id, preferences)

    assert {:ok, route_1} = OmeglePhoenix.SessionManager.get_session_route(session_id)
    assert {:ok, route_2} = OmeglePhoenix.SessionManager.get_session_route(peer_session_id)

    membership_key_1 = OmeglePhoenix.RedisKeys.session_queue_key(session_id, route_1)
    membership_key_2 = OmeglePhoenix.RedisKeys.session_queue_key(peer_session_id, route_2)

    assert_eventually(fn ->
      with {:ok, queue_keys_1} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1]),
           {:ok, queue_keys_2} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]) do
        queue_keys_1 != [] and queue_keys_2 != []
      else
        _ -> false
      end
    end)

    assert {:ok, queue_keys} = OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1])
    GenServer.cast(OmeglePhoenix.Matchmaker, {:schedule_local_match_attempts, queue_keys})

    assert_eventually(fn ->
      with {:ok, matched_1} <- OmeglePhoenix.SessionManager.get_session(session_id),
           {:ok, matched_2} <- OmeglePhoenix.SessionManager.get_session(peer_session_id) do
        matched_1.status == :matched and matched_1.partner_id == peer_session_id and
          matched_2.status == :matched and matched_2.partner_id == session_id
      else
        _ -> false
      end
    end)

    assert_eventually(fn ->
      OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1]) == {:ok, []} and
        OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]) == {:ok, []}
    end)
  end

  test "pairing crash before session write leaves waiting users queued", %{
    session_id: session_id,
    peer_session_id: peer_session_id,
    ip: ip,
    peer_ip: peer_ip
  } do
    preferences = %{"mode" => "text", "interests" => "crash,window"}
    hook_ref = make_ref()

    original_hooks =
      install_cluster_pairing_test_hook({self(), hook_ref, :once, :before_pair_sessions})

    on_exit(fn ->
      restore_cluster_pairing_test_hook(original_hooks)
    end)

    assert {:ok, _session_1} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, _session_2} =
             OmeglePhoenix.SessionManager.create_session(peer_session_id, peer_ip, preferences)

    assert {:ok, session_1} = OmeglePhoenix.SessionManager.get_session(session_id)

    assert {:ok, _moved_session_2} =
             OmeglePhoenix.SessionManager.move_session_shard(
               peer_session_id,
               session_1.redis_shard
             )

    assert :ok = OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)
    assert :ok = OmeglePhoenix.Matchmaker.join_queue(peer_session_id, preferences)

    assert {:ok, route_1} = OmeglePhoenix.SessionManager.get_session_route(session_id)
    assert {:ok, route_2} = OmeglePhoenix.SessionManager.get_session_route(peer_session_id)

    membership_key_1 = OmeglePhoenix.RedisKeys.session_queue_key(session_id, route_1)
    membership_key_2 = OmeglePhoenix.RedisKeys.session_queue_key(peer_session_id, route_2)

    assert_eventually(fn ->
      with {:ok, queue_keys_1} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1]),
           {:ok, queue_keys_2} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]) do
        queue_keys_1 != [] and queue_keys_2 != []
      else
        _ -> false
      end
    end)

    assert {:ok, queue_keys} = OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1])
    GenServer.cast(OmeglePhoenix.Matchmaker, {:schedule_local_match_attempts, queue_keys})

    assert_receive(
      {:matchmaker_pairing_hook, ^hook_ref, :before_pair_sessions, first_id, second_id, task_pid},
      5_000
    )

    assert Enum.sort([first_id, second_id]) == Enum.sort([session_id, peer_session_id])

    send(task_pid, {:matchmaker_pairing_hook_reply, hook_ref, {:exit, :injected_pairing_crash}})

    assert_eventually(fn ->
      with {:ok, waiting_1} <- OmeglePhoenix.SessionManager.get_session(session_id),
           {:ok, waiting_2} <- OmeglePhoenix.SessionManager.get_session(peer_session_id),
           {:ok, queue_keys_1} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1]),
           {:ok, queue_keys_2} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]) do
        waiting_1.status == :waiting and is_nil(waiting_1.partner_id) and
          waiting_2.status == :waiting and is_nil(waiting_2.partner_id) and
          queue_keys_1 != [] and queue_keys_2 != []
      else
        _ -> false
      end
    end)
  end

  test "removing the second candidate mid-pair still retries the first session in the same sweep",
       %{
         session_id: session_id,
         peer_session_id: peer_session_id,
         ip: ip,
         peer_ip: peer_ip
       } do
    third_session_id = UUID.uuid4()
    third_ip = next_test_ip()
    preferences = %{"mode" => "text", "interests" => "retry,second"}
    hook_ref = make_ref()

    on_exit(fn -> cleanup_session(third_session_id) end)

    original_hooks =
      install_cluster_pairing_test_hook({self(), hook_ref, :once, :before_load_sessions})

    on_exit(fn ->
      restore_cluster_pairing_test_hook(original_hooks)
    end)

    {route_1, route_2, route_3} =
      prepare_three_queued_sessions(
        {session_id, ip},
        {peer_session_id, peer_ip},
        {third_session_id, third_ip},
        preferences
      )

    membership_key_1 = OmeglePhoenix.RedisKeys.session_queue_key(session_id, route_1)
    membership_key_2 = OmeglePhoenix.RedisKeys.session_queue_key(peer_session_id, route_2)
    membership_key_3 = OmeglePhoenix.RedisKeys.session_queue_key(third_session_id, route_3)

    assert {:ok, queue_keys} = OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1])
    GenServer.cast(OmeglePhoenix.Matchmaker, {:schedule_local_match_attempts, queue_keys})

    assert_receive(
      {:matchmaker_pairing_hook, ^hook_ref, :before_load_sessions, first_id, second_id, task_pid},
      5_000
    )

    assert {first_id, second_id} == {session_id, peer_session_id}

    assert {:ok, %{id: ^peer_session_id, ban_status: true}} =
             OmeglePhoenix.SessionManager.emergency_ban(peer_session_id, "retry-second-test")

    send(task_pid, {:matchmaker_pairing_hook_reply, hook_ref, :continue})

    wait_for_local_match_batch_idle()

    assert {:ok, matched_1} = OmeglePhoenix.SessionManager.get_session(session_id)
    assert {:ok, matched_3} = OmeglePhoenix.SessionManager.get_session(third_session_id)
    assert {:ok, banned_2} = OmeglePhoenix.SessionManager.get_session(peer_session_id)

    assert matched_1.status == :matched
    assert matched_1.partner_id == third_session_id
    assert matched_3.status == :matched
    assert matched_3.partner_id == session_id
    assert banned_2.ban_status

    assert_eventually(fn ->
      OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1]) == {:ok, []} and
        OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]) == {:ok, []} and
        OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_3]) == {:ok, []}
    end)
  end

  test "making the first candidate unpairable mid-pair still retries the second session in the same sweep",
       %{
         session_id: session_id,
         peer_session_id: peer_session_id,
         ip: ip,
         peer_ip: peer_ip
       } do
    third_session_id = UUID.uuid4()
    third_ip = next_test_ip()
    preferences = %{"mode" => "text", "interests" => "retry,first"}
    hook_ref = make_ref()

    on_exit(fn -> cleanup_session(third_session_id) end)

    original_hooks =
      install_cluster_pairing_test_hook({self(), hook_ref, :once, :before_load_sessions})

    on_exit(fn ->
      restore_cluster_pairing_test_hook(original_hooks)
    end)

    {route_1, route_2, route_3} =
      prepare_three_queued_sessions(
        {session_id, ip},
        {peer_session_id, peer_ip},
        {third_session_id, third_ip},
        preferences
      )

    membership_key_1 = OmeglePhoenix.RedisKeys.session_queue_key(session_id, route_1)
    membership_key_2 = OmeglePhoenix.RedisKeys.session_queue_key(peer_session_id, route_2)
    membership_key_3 = OmeglePhoenix.RedisKeys.session_queue_key(third_session_id, route_3)

    assert {:ok, queue_keys} = OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1])
    GenServer.cast(OmeglePhoenix.Matchmaker, {:schedule_local_match_attempts, queue_keys})

    assert_receive(
      {:matchmaker_pairing_hook, ^hook_ref, :before_load_sessions, first_id, second_id, task_pid},
      5_000
    )

    assert {first_id, second_id} == {session_id, peer_session_id}

    assert {:ok, _updated_session} =
             OmeglePhoenix.SessionManager.update_session(session_id, %{
               status: :matched,
               partner_id: "external-partner"
             })

    send(task_pid, {:matchmaker_pairing_hook_reply, hook_ref, :continue})

    wait_for_local_match_batch_idle()

    assert {:ok, updated_1} = OmeglePhoenix.SessionManager.get_session(session_id)
    assert {:ok, matched_2} = OmeglePhoenix.SessionManager.get_session(peer_session_id)
    assert {:ok, matched_3} = OmeglePhoenix.SessionManager.get_session(third_session_id)

    assert updated_1.status == :matched
    assert updated_1.partner_id == "external-partner"
    assert matched_2.status == :matched
    assert matched_2.partner_id == third_session_id
    assert matched_3.status == :matched
    assert matched_3.partner_id == peer_session_id

    assert_eventually(fn ->
      OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]) == {:ok, []} and
        OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_3]) == {:ok, []}
    end)
  end

  test "router delivers directly to a remote owner across connected phoenix nodes", %{
    session_id: session_id,
    ip: ip
  } do
    preferences = %{"mode" => "text", "interests" => "beam,cluster"}
    peer_node = connected_peer_node!()

    assert {:ok, _session} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)

    probe_path = "/app/lib/omegle_phoenix/router_probe.ex"
    assert {_module, _binary} = hd(:rpc.call(peer_node, Code, :compile_file, [probe_path]))

    assert {:ok, remote_probe} =
             :rpc.call(peer_node, OmeglePhoenix.RouterProbe, :start, [session_id, self()])

    on_exit(fn ->
      if is_pid(remote_probe) do
        send(remote_probe, {:router_probe_stop, self()})
        assert_receive {:router_probe_stopped, ^session_id, ^peer_node}, 5_000
      end
    end)

    assert_receive {:router_probe_registered, ^session_id, ^peer_node, ^remote_probe}, 5_000

    assert_eventually(fn ->
      OmeglePhoenix.Router.owner_node(session_id, route_hint: route) ==
        {:ok, Atom.to_string(peer_node)}
    end)

    OmeglePhoenix.Router.send_message(
      session_id,
      %{
        type: "message",
        from: "integration-test",
        match_generation: "gen-1",
        data: %{content: "hello from cluster"}
      },
      route_hint: route,
      owner_hint: Atom.to_string(peer_node)
    )

    assert_receive(
      {:router_probe_message, ^session_id,
       %{
         type: "message",
         from: "integration-test",
         match_generation: "gen-1",
         data: %{content: "hello from cluster"}
       }, ^peer_node},
      5_000
    )
  end

  test "ip ban and unban works through the session manager", %{
    session_id: session_id,
    ip: ip
  } do
    assert {:ok, _session} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, %{"mode" => "text"})

    assert {:ok, banned_ids} = OmeglePhoenix.SessionManager.emergency_ban_ip(ip, "test ban")
    assert session_id in banned_ids
    assert OmeglePhoenix.SessionManager.ip_ban_reason(ip) == {:ok, "test ban"}

    assert :ok = OmeglePhoenix.SessionManager.emergency_unban_ip(ip)
    assert {:ok, session} = OmeglePhoenix.SessionManager.get_session(session_id)
    refute session.ban_status
    assert session.ban_reason == nil
  end

  test "pair and reset flows update both sessions end to end", %{
    session_id: session_id,
    peer_session_id: peer_session_id,
    ip: ip,
    peer_ip: peer_ip
  } do
    preferences_1 = %{"mode" => "text", "interests" => "elixir,redis"}
    preferences_2 = %{"mode" => "text", "interests" => "elixir,phoenix"}

    assert {:ok, _} = OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences_1)

    assert {:ok, _} =
             OmeglePhoenix.SessionManager.create_session(peer_session_id, peer_ip, preferences_2)

    assert {:ok, session_1} = OmeglePhoenix.SessionManager.get_session(session_id)
    assert {:ok, session_2} = OmeglePhoenix.SessionManager.get_session(peer_session_id)

    assert {:ok, _updated_1, _updated_2, common_interests} =
             OmeglePhoenix.SessionManager.pair_sessions(session_1, session_2)

    assert "elixir" in common_interests

    assert_eventually(fn ->
      with {:ok, paired_1} <- OmeglePhoenix.SessionManager.get_session(session_id),
           {:ok, paired_2} <- OmeglePhoenix.SessionManager.get_session(peer_session_id) do
        paired_1.status == :matched and paired_1.partner_id == peer_session_id and
          paired_2.status == :matched and paired_2.partner_id == session_id
      else
        _ -> false
      end
    end)

    assert {:matched, partner_session} = OmeglePhoenix.Matchmaker.check_match(session_id)
    assert partner_session.id == peer_session_id

    assert {:ok, paired_1} = OmeglePhoenix.SessionManager.get_session(session_id)
    assert {:ok, paired_2} = OmeglePhoenix.SessionManager.get_session(peer_session_id)
    assert {:ok, _reset_1, _reset_2} = OmeglePhoenix.SessionManager.reset_pair(paired_1, paired_2)

    assert_eventually(fn ->
      with {:ok, reset_1} <- OmeglePhoenix.SessionManager.get_session(session_id),
           {:ok, reset_2} <- OmeglePhoenix.SessionManager.get_session(peer_session_id) do
        reset_1.status == :waiting and is_nil(reset_1.partner_id) and
          reset_2.status == :waiting and is_nil(reset_2.partner_id)
      else
        _ -> false
      end
    end)
  end

  test "emergency disconnect resets the partner safely", %{
    session_id: session_id,
    peer_session_id: peer_session_id,
    ip: ip,
    peer_ip: peer_ip
  } do
    preferences = %{"mode" => "text", "interests" => "beam,cluster"}

    assert {:ok, _} = OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, _} =
             OmeglePhoenix.SessionManager.create_session(peer_session_id, peer_ip, preferences)

    assert {:ok, session_1} = OmeglePhoenix.SessionManager.get_session(session_id)
    assert {:ok, session_2} = OmeglePhoenix.SessionManager.get_session(peer_session_id)

    assert {:ok, _updated_1, _updated_2, _common} =
             OmeglePhoenix.SessionManager.pair_sessions(session_1, session_2)

    assert {:ok, %{id: ^session_id}} =
             OmeglePhoenix.SessionManager.emergency_disconnect(session_id)

    assert_eventually(fn ->
      with {:ok, disconnected} <- OmeglePhoenix.SessionManager.get_session(session_id),
           {:ok, partner} <- OmeglePhoenix.SessionManager.get_session(peer_session_id) do
        disconnected.status == :disconnecting and is_nil(disconnected.partner_id) and
          partner.status == :waiting and is_nil(partner.partner_id)
      else
        _ -> false
      end
    end)
  end

  test "reaper cleans orphaned active session entries", %{session_id: session_id, ip: ip} do
    preferences = %{"mode" => "text", "interests" => "orphan,reaper"}

    assert {:ok, _session} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, preferences)

    assert {:ok, route} = OmeglePhoenix.SessionManager.get_session_route(session_id)

    assert {:ok, active_sessions} =
             OmeglePhoenix.Redis.command([
               "SMEMBERS",
               OmeglePhoenix.RedisKeys.active_sessions_key()
             ])

    assert session_id in active_sessions

    assert {:ok, 1} =
             OmeglePhoenix.Redis.command([
               "DEL",
               OmeglePhoenix.RedisKeys.session_key(session_id, route)
             ])

    send(OmeglePhoenix.Reaper, :reap)

    assert_eventually(fn ->
      case OmeglePhoenix.Redis.command(["SMEMBERS", OmeglePhoenix.RedisKeys.active_sessions_key()]) do
        {:ok, active_sessions} when is_list(active_sessions) -> session_id not in active_sessions
        _ -> false
      end
    end)

    assert_eventually(fn ->
      OmeglePhoenix.SessionManager.get_session(session_id) == {:error, :not_found}
    end)
  end

  defp ensure_live_env! do
    if not @live_enabled do
      raise "LIVE_REDIS_CLUSTER_TESTS is not enabled for live Redis integration tests"
    end

    missing =
      Enum.reject(@required_env, fn key ->
        value = System.get_env(key)
        is_binary(value) and String.trim(value) != ""
      end)

    if missing != [] do
      raise "Missing required env vars for live Redis integration tests: #{Enum.join(missing, ", ")}"
    end
  end

  defp ensure_distributed_test_node! do
    if not Node.alive?() do
      ensure_epmd_started!()
      unique_name = :"redis_live_test_#{System.unique_integer([:positive])}"

      case Node.start(unique_name, :shortnames) do
        {:ok, _pid} ->
          :ok

        {:error, {:already_started, _pid}} ->
          :ok

        {:error, reason} ->
          raise "Failed to start distributed test node: #{inspect(reason)}"
      end
    end

    cookie =
      case System.get_env("NODE_COOKIE") do
        nil -> raise "NODE_COOKIE must be set for distributed live Redis tests"
        value -> String.to_atom(value)
      end

    true = Node.set_cookie(cookie)
    :ok
  end

  defp ensure_epmd_started! do
    case System.cmd("epmd", ["-daemon"], stderr_to_stdout: true) do
      {_output, 0} ->
        :ok

      {output, status} ->
        raise "Failed to start epmd for distributed live Redis tests (status #{status}): #{String.trim(output)}"
    end
  end

  defp ensure_cluster_connected! do
    peers =
      System.get_env("CLUSTER_NODES", "")
      |> String.split(",", trim: true)
      |> Enum.map(&String.trim/1)
      |> Enum.reject(&(&1 == ""))
      |> Enum.map(&String.to_atom/1)
      |> Enum.reject(&(&1 == Node.self()))

    Enum.each(peers, fn peer ->
      _ = Node.connect(peer)
    end)

    assert_eventually(fn ->
      Enum.all?(peers, &(&1 in Node.list()))
    end)
  end

  defp connected_peer_node! do
    assert_eventually(fn -> Node.list() != [] end)
    Enum.at(Node.list(), 0)
  end

  defp prepare_three_queued_sessions(
         {session_id_1, ip_1},
         {session_id_2, ip_2},
         {session_id_3, ip_3},
         preferences
       ) do
    assert {:ok, _session_1} =
             OmeglePhoenix.SessionManager.create_session(session_id_1, ip_1, preferences)

    assert {:ok, _session_2} =
             OmeglePhoenix.SessionManager.create_session(session_id_2, ip_2, preferences)

    assert {:ok, _session_3} =
             OmeglePhoenix.SessionManager.create_session(session_id_3, ip_3, preferences)

    assert {:ok, session_1} = OmeglePhoenix.SessionManager.get_session(session_id_1)

    assert {:ok, _moved_session_2} =
             OmeglePhoenix.SessionManager.move_session_shard(session_id_2, session_1.redis_shard)

    assert {:ok, _moved_session_3} =
             OmeglePhoenix.SessionManager.move_session_shard(session_id_3, session_1.redis_shard)

    join_queue_in_order(session_id_1, preferences)
    join_queue_in_order(session_id_2, preferences)
    join_queue_in_order(session_id_3, preferences)

    assert {:ok, route_1} = OmeglePhoenix.SessionManager.get_session_route(session_id_1)
    assert {:ok, route_2} = OmeglePhoenix.SessionManager.get_session_route(session_id_2)
    assert {:ok, route_3} = OmeglePhoenix.SessionManager.get_session_route(session_id_3)

    membership_key_1 = OmeglePhoenix.RedisKeys.session_queue_key(session_id_1, route_1)
    membership_key_2 = OmeglePhoenix.RedisKeys.session_queue_key(session_id_2, route_2)
    membership_key_3 = OmeglePhoenix.RedisKeys.session_queue_key(session_id_3, route_3)

    assert_eventually(fn ->
      with {:ok, queue_keys_1} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_1]),
           {:ok, queue_keys_2} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_2]),
           {:ok, queue_keys_3} <- OmeglePhoenix.Redis.command(["SMEMBERS", membership_key_3]) do
        queue_keys_1 != [] and queue_keys_2 != [] and queue_keys_3 != []
      else
        _ -> false
      end
    end)

    {route_1, route_2, route_3}
  end

  defp join_queue_in_order(session_id, preferences) do
    assert :ok = OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)
    Process.sleep(5)
    :ok
  end

  defp install_cluster_pairing_test_hook(hook) do
    pairing_test_hook_nodes()
    |> Enum.map(fn node ->
      original_hook =
        rpc_call!(node, Application, :get_env, [:omegle_phoenix, :matchmaker_pairing_test_hook])

      :ok =
        rpc_call!(node, Application, :put_env, [
          :omegle_phoenix,
          :matchmaker_pairing_test_hook,
          hook
        ])

      {node, original_hook}
    end)
  end

  defp restore_cluster_pairing_test_hook(original_hooks) do
    Enum.each(original_hooks, fn {node, original_hook} ->
      if original_hook do
        :ok =
          rpc_call!(node, Application, :put_env, [
            :omegle_phoenix,
            :matchmaker_pairing_test_hook,
            original_hook
          ])
      else
        :ok =
          rpc_call!(node, Application, :delete_env, [
            :omegle_phoenix,
            :matchmaker_pairing_test_hook
          ])
      end
    end)
  end

  defp pairing_test_hook_nodes do
    [Node.self() | Node.list()]
    |> Enum.uniq()
  end

  defp rpc_call!(node, module, function, args) do
    case :rpc.call(node, module, function, args) do
      {:badrpc, reason} ->
        flunk("RPC to #{inspect(node)} failed for #{inspect(module)}.#{function}: #{inspect(reason)}")

      result ->
        result
    end
  end

  defp wait_for_local_match_batch_idle do
    assert_eventually(fn ->
      state = :sys.get_state(OmeglePhoenix.Matchmaker)
      is_nil(state.local_match_batch_ref) and MapSet.size(state.pending_local_match_keys) == 0
    end)
  end

  defp cleanup_session(session_id) do
    _ =
      case OmeglePhoenix.SessionManager.delete_session(session_id) do
        :ok -> :ok
        {:error, :not_found} -> :ok
        _ -> :ok
      end

    _ = OmeglePhoenix.SessionManager.cleanup_orphaned_session(session_id)
    :ok
  end

  defp cleanup_ip_ban(ip) do
    _ = OmeglePhoenix.Redis.command(["DEL", "ban:ip:#{ip}"])
    :ok
  end

  defp next_test_ip do
    suffix = System.unique_integer([:positive])
    "203.0.113.#{rem(suffix, 200) + 1}"
  end

  defp assert_eventually(fun, attempts \\ 30)

  defp assert_eventually(fun, attempts) when attempts > 0 do
    if fun.() do
      :ok
    else
      Process.sleep(100)
      assert_eventually(fun, attempts - 1)
    end
  end

  defp assert_eventually(_fun, 0), do: flunk("condition not met before timeout")
end
