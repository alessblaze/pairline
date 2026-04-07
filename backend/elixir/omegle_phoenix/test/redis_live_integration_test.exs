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
    Application.ensure_all_started(:omegle_phoenix)
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

    assert OmeglePhoenix.Redis.command(["GET", OmeglePhoenix.RedisKeys.session_locator_key(session_id)]) ==
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

  test "ip ban and unban works through the session manager", %{
    session_id: session_id,
    ip: ip
  } do
    assert {:ok, _session} =
             OmeglePhoenix.SessionManager.create_session(session_id, ip, %{"mode" => "text"})

    assert {:ok, banned_ids} = OmeglePhoenix.SessionManager.emergency_ban_ip(ip, "test ban")
    assert session_id in banned_ids
    assert OmeglePhoenix.SessionManager.ip_ban_reason(ip) == "test ban"

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
