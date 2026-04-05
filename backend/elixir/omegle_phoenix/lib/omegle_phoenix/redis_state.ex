defmodule OmeglePhoenix.RedisState do
  @moduledoc false

  @queue_meta_key_prefix "session"

  @persist_script """
  redis.call('SETEX', KEYS[1], ARGV[1], ARGV[5])
  redis.call('SETEX', KEYS[2], ARGV[1], ARGV[3])
  redis.call('SETEX', KEYS[3], ARGV[1], ARGV[4])
  redis.call('SADD', KEYS[4], ARGV[2])
  redis.call('SADD', KEYS[5], ARGV[2])
  redis.call('SETEX', KEYS[7], ARGV[1], ARGV[6])
  if redis.call('EXISTS', KEYS[6]) == 1 then
    redis.call('EXPIRE', KEYS[6], ARGV[1])
  end
  return 1
  """

  @delete_script """
  redis.call('SREM', KEYS[1], ARGV[1])
  redis.call('SREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3], KEYS[6], KEYS[7], KEYS[9])
  if redis.call('EXISTS', KEYS[4]) == 1 then
    redis.call('EXPIRE', KEYS[4], ARGV[2])
  end
  if redis.call('EXISTS', KEYS[5]) == 1 then
    redis.call('EXPIRE', KEYS[5], ARGV[2])
  end
  if redis.call('EXISTS', KEYS[8]) == 1 then
    redis.call('EXPIRE', KEYS[8], ARGV[2])
  end
  if redis.call('SCARD', KEYS[2]) == 0 then
    redis.call('DEL', KEYS[2])
  end
  return 1
  """

  @pair_script """
  redis.call('SETEX', KEYS[1], ARGV[1], ARGV[5])
  redis.call('SETEX', KEYS[2], ARGV[1], ARGV[3])
  redis.call('SETEX', KEYS[3], ARGV[1], ARGV[4])
  redis.call('SETEX', KEYS[4], ARGV[1], ARGV[6])
  redis.call('SETEX', KEYS[6], ARGV[1], ARGV[11])
  redis.call('SETEX', KEYS[7], ARGV[1], ARGV[9])
  redis.call('SETEX', KEYS[8], ARGV[1], ARGV[10])
  redis.call('SETEX', KEYS[9], ARGV[1], ARGV[12])
  redis.call('SADD', KEYS[11], ARGV[2], ARGV[8])
  redis.call('SADD', KEYS[12], ARGV[2])
  redis.call('SADD', KEYS[13], ARGV[8])
  if redis.call('EXISTS', KEYS[5]) == 1 then redis.call('EXPIRE', KEYS[5], ARGV[1]) end
  if redis.call('EXISTS', KEYS[10]) == 1 then redis.call('EXPIRE', KEYS[10], ARGV[1]) end
  redis.call('SETEX', KEYS[14], ARGV[1], ARGV[8])
  redis.call('SETEX', KEYS[15], ARGV[1], ARGV[2])
  redis.call('SETEX', KEYS[16], ARGV[7], ARGV[8])
  redis.call('SETEX', KEYS[17], ARGV[7], ARGV[2])
  return 1
  """

  # KEYS
  # 1  session1:data
  # 2  session1:ip
  # 3  session1:token
  # 4  session1:queue_meta
  # 5  session1:owner
  # 6  session2:data
  # 7  session2:ip
  # 8  session2:token
  # 9  session2:queue_meta
  # 10 session2:owner
  # 11 active_sessions
  # 12 ip_sessions(session1.ip)
  # 13 ip_sessions(session2.ip)
  # 14 match(session1.id)
  # 15 match(session2.id)
  # 16 recent_match(session1.id)
  # 17 recent_match(session2.id)
  #
  # ARGV
  # 1 ttl
  # 2 session1.id
  # 3 session1.ip
  # 4 session1.hashed_token
  # 5 session1.encoded
  # 6 session1.queue_meta
  # 7 recent_ttl
  # 8 session2.id
  # 9 session2.ip
  # 10 session2.hashed_token
  # 11 session2.encoded
  # 12 session2.queue_meta
  @reset_pair_script """
  redis.call('SETEX', KEYS[1], ARGV[1], ARGV[5])
  redis.call('SETEX', KEYS[2], ARGV[1], ARGV[3])
  redis.call('SETEX', KEYS[3], ARGV[1], ARGV[4])
  redis.call('SETEX', KEYS[4], ARGV[1], ARGV[6])

  redis.call('SETEX', KEYS[6], ARGV[1], ARGV[10])
  redis.call('SETEX', KEYS[7], ARGV[1], ARGV[8])
  redis.call('SETEX', KEYS[8], ARGV[1], ARGV[9])
  redis.call('SETEX', KEYS[9], ARGV[1], ARGV[11])

  if redis.call('EXISTS', KEYS[5]) == 1 then
    redis.call('EXPIRE', KEYS[5], ARGV[1])
  end

  if redis.call('EXISTS', KEYS[10]) == 1 then
    redis.call('EXPIRE', KEYS[10], ARGV[1])
  end

  redis.call('SADD', KEYS[11], ARGV[2], ARGV[7])
  redis.call('SADD', KEYS[12], ARGV[2])
  redis.call('SADD', KEYS[13], ARGV[7])

  redis.call('DEL', KEYS[14], KEYS[15])
  return 1
  """

  # Atomically ban a session: checks idempotency, sets ban fields, nils partner_id,
  # cleans up match key, and returns the old partner_id (or "nil"/"already_banned"/"not_found").
  # KEYS[1] = session:data:<session_id>
  # KEYS[2] = match:<session_id>
  # ARGV[1] = ttl, ARGV[2] = ban_reason, ARGV[3] = current_timestamp
  @emergency_ban_script """
  local data = redis.call('GET', KEYS[1])
  if not data then
    return "not_found"
  end
  local session = cjson.decode(data)
  if session["ban_status"] == true then
    return "already_banned"
  end
  local old_partner_id = session["partner_id"]
  session["ban_status"] = true
  session["ban_reason"] = ARGV[2]
  session["status"] = "disconnecting"
  session["partner_id"] = cjson.null
  session["last_activity"] = tonumber(ARGV[3])
  redis.call('SETEX', KEYS[1], ARGV[1], cjson.encode(session))
  redis.call('DEL', KEYS[2])
  if old_partner_id and type(old_partner_id) == "string" then
    return old_partner_id
  else
    return "nil"
  end
  """

  # Atomically disconnect a session (admin action): sets status to disconnecting,
  # nils partner_id, returns old partner_id.
  # KEYS[1] = session:data:<session_id>
  # KEYS[2] = match:<session_id>
  # ARGV[1] = ttl, ARGV[2] = current_timestamp
  @emergency_disconnect_script """
  local data = redis.call('GET', KEYS[1])
  if not data then
    return "not_found"
  end
  local session = cjson.decode(data)
  local old_partner_id = session["partner_id"]
  session["status"] = "disconnecting"
  session["partner_id"] = cjson.null
  session["last_activity"] = tonumber(ARGV[2])
  redis.call('SETEX', KEYS[1], ARGV[1], cjson.encode(session))
  redis.call('DEL', KEYS[2])
  if old_partner_id and type(old_partner_id) == "string" then
    return old_partner_id
  else
    return "nil"
  end
  """

  # Atomically disconnect a partner: only resets the partner session if their
  # partner_id still points at the expected peer (prevents disrupting a new match
  # formed during the race window). Cleans up the partner's match key.
  # KEYS[1] = session:data:<partner_id>
  # KEYS[2] = match:<partner_id>
  # ARGV[1] = ttl, ARGV[2] = current_timestamp, ARGV[3] = expected_peer_id
  @disconnect_partner_script """
  local data = redis.call('GET', KEYS[1])
  if not data then
    return "not_found"
  end
  local session = cjson.decode(data)
  local current_partner = session["partner_id"]
  if type(current_partner) ~= "string" or current_partner ~= ARGV[3] then
    return "partner_changed"
  end
  session["partner_id"] = cjson.null
  session["status"] = "waiting"
  session["signaling_ready"] = false
  session["webrtc_started"] = false
  session["last_activity"] = tonumber(ARGV[2])
  redis.call('SETEX', KEYS[1], ARGV[1], cjson.encode(session))
  redis.call('DEL', KEYS[2])
  return "ok"
  """

  # Refreshes session TTL and last_activity without a full read-modify-write
  # round-trip through the BEAM. Also refreshes related hot keys.
  @touch_session_script """
  local data = redis.call('GET', KEYS[1])
  if not data then
    return "not_found"
  end
  local session = cjson.decode(data)
  session["last_activity"] = tonumber(ARGV[2])
  redis.call('SETEX', KEYS[1], ARGV[1], cjson.encode(session))
  if redis.call('EXISTS', KEYS[2]) == 1 then
    redis.call('EXPIRE', KEYS[2], ARGV[1])
  end
  if redis.call('EXISTS', KEYS[3]) == 1 then
    redis.call('EXPIRE', KEYS[3], ARGV[1])
  end
  if redis.call('EXISTS', KEYS[4]) == 1 then
    redis.call('EXPIRE', KEYS[4], ARGV[1])
  end
  if redis.call('EXISTS', KEYS[5]) == 1 then
    redis.call('EXPIRE', KEYS[5], ARGV[1])
  end
  if redis.call('EXISTS', KEYS[6]) == 1 then
    redis.call('EXPIRE', KEYS[6], ARGV[1])
  end
  return "ok"
  """

  def persist_session(session, ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)
    hashed_token = hashed_token(session.token)

    command = [
      "EVAL",
      @persist_script,
      "7",
      session_key(session.id),
      session_ip_key(session.id),
      session_token_key(session.id),
      active_sessions_key(),
      ip_sessions_key(session.ip),
      session_owner_key(session.id),
      queue_meta_key(session.id),
      ttl,
      session.id,
      session.ip,
      hashed_token,
      encode_session(session),
      encode_queue_meta(session)
    ]

    exec(command, opts)
  end

  def delete_session(session_id, ip, report_grace_seconds, opts \\ []) do
    report_grace_ttl = normalize_ttl!(report_grace_seconds)

    command = [
      "EVAL",
      @delete_script,
      "9",
      active_sessions_key(),
      ip_sessions_key(ip),
      session_key(session_id),
      session_ip_key(session_id),
      session_token_key(session_id),
      session_owner_key(session_id),
      match_key(session_id),
      recent_match_key(session_id),
      queue_meta_key(session_id),
      session_id,
      report_grace_ttl
    ]

    exec(command, opts)
  end

  def pair_sessions(session1, session2, ttl_seconds, recent_ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)
    recent_ttl = normalize_ttl!(recent_ttl_seconds)

    command = [
      "EVAL",
      @pair_script,
      "17",
      session_key(session1.id),
      session_ip_key(session1.id),
      session_token_key(session1.id),
      queue_meta_key(session1.id),
      session_owner_key(session1.id),
      session_key(session2.id),
      session_ip_key(session2.id),
      session_token_key(session2.id),
      queue_meta_key(session2.id),
      session_owner_key(session2.id),
      active_sessions_key(),
      ip_sessions_key(session1.ip),
      ip_sessions_key(session2.ip),
      match_key(session1.id),
      match_key(session2.id),
      recent_match_key(session1.id),
      recent_match_key(session2.id),
      ttl,
      session1.id,
      session1.ip,
      hashed_token(session1.token),
      encode_session(session1),
      encode_queue_meta(session1),
      recent_ttl,
      session2.id,
      session2.ip,
      hashed_token(session2.token),
      encode_session(session2),
      encode_queue_meta(session2)
    ]

    exec(command, opts)
  end

  def reset_pair(session1, session2, ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)

    command = [
      "EVAL",
      @reset_pair_script,
      "15",
      session_key(session1.id),
      session_ip_key(session1.id),
      session_token_key(session1.id),
      queue_meta_key(session1.id),
      session_owner_key(session1.id),
      session_key(session2.id),
      session_ip_key(session2.id),
      session_token_key(session2.id),
      queue_meta_key(session2.id),
      session_owner_key(session2.id),
      active_sessions_key(),
      ip_sessions_key(session1.ip),
      ip_sessions_key(session2.ip),
      match_key(session1.id),
      match_key(session2.id),
      ttl,
      session1.id,
      session1.ip,
      hashed_token(session1.token),
      encode_session(session1),
      encode_queue_meta(session1),
      session2.id,
      session2.ip,
      hashed_token(session2.token),
      encode_session(session2),
      encode_queue_meta(session2)
    ]

    exec(command, opts)
  end

  def atomic_emergency_ban(session_id, reason, ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)
    now = Integer.to_string(System.system_time(:second))

    command = [
      "EVAL",
      @emergency_ban_script,
      "2",
      session_key(session_id),
      match_key(session_id),
      ttl,
      reason,
      now
    ]

    exec(command, opts)
  end

  def atomic_emergency_disconnect(session_id, ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)
    now = Integer.to_string(System.system_time(:second))

    command = [
      "EVAL",
      @emergency_disconnect_script,
      "2",
      session_key(session_id),
      match_key(session_id),
      ttl,
      now
    ]

    exec(command, opts)
  end

  def atomic_disconnect_partner(partner_id, expected_peer_id, ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)
    now = Integer.to_string(System.system_time(:second))

    command = [
      "EVAL",
      @disconnect_partner_script,
      "2",
      session_key(partner_id),
      match_key(partner_id),
      ttl,
      now,
      expected_peer_id
    ]

    exec(command, opts)
  end

  def touch_session(session_id, ttl_seconds, opts \\ []) do
    ttl = normalize_ttl!(ttl_seconds)
    now = Integer.to_string(System.system_time(:second))

    command = [
      "EVAL",
      @touch_session_script,
      "6",
      session_key(session_id),
      session_ip_key(session_id),
      session_token_key(session_id),
      session_owner_key(session_id),
      match_key(session_id),
      queue_meta_key(session_id),
      ttl,
      now
    ]

    exec(command, opts)
  end

  def cleanup_orphaned_session(session_id, ip \\ nil, report_grace_or_opts \\ nil, opts \\ [])

  def cleanup_orphaned_session(session_id, ip, opts, [])
      when is_list(opts) or is_nil(opts) do
    cleanup_orphaned_session(
      session_id,
      ip,
      OmeglePhoenix.Config.get_report_grace_seconds(),
      opts || []
    )
  end

  def cleanup_orphaned_session(session_id, ip, report_grace_seconds, opts) do
    ip_value =
      case ip do
        nil ->
          case OmeglePhoenix.Redis.command(["GET", session_ip_key(session_id)]) do
            {:ok, value} when is_binary(value) -> value
            _ -> "unknown"
          end

        value ->
          value
      end

    delete_session(session_id, ip_value, report_grace_seconds, opts)
  end

  defp exec(command, opts) do
    case OmeglePhoenix.Redis.command(command) do
      {:ok, _result} = ok ->
        if Keyword.get(opts, :telemetry, true) do
          :telemetry.execute(
            [:omegle_phoenix, :redis_state, :success],
            %{count: 1},
            %{operation: hd(command)}
          )
        end

        ok

      {:error, reason} = error ->
        if Keyword.get(opts, :telemetry, true) do
          :telemetry.execute(
            [:omegle_phoenix, :redis_state, :failure],
            %{count: 1},
            %{operation: hd(command), reason: inspect(reason)}
          )
        end

        error
    end
  end

  defp normalize_ttl!(ttl) when is_integer(ttl) and ttl > 0, do: Integer.to_string(ttl)

  defp normalize_ttl!(ttl) when is_binary(ttl) do
    case Integer.parse(ttl) do
      {n, ""} when n > 0 -> Integer.to_string(n)
      _ -> raise ArgumentError, "invalid Redis TTL: #{inspect(ttl)}"
    end
  end

  defp normalize_ttl!(ttl), do: raise(ArgumentError, "invalid Redis TTL: #{inspect(ttl)}")

  defp encode_session(session), do: session |> serialize_session() |> Jason.encode!()
  defp encode_queue_meta(session), do: session |> build_queue_meta() |> Jason.encode!()

  defp serialize_session(session) do
    Enum.reduce(Map.keys(session), %{}, fn field, acc ->
      value =
        case Map.get(session, field) do
          value when field == :status and is_atom(value) -> Atom.to_string(value)
          value -> value
        end

      Map.put(acc, Atom.to_string(field), value)
    end)
  end

  defp build_queue_meta(session) do
    preferences = Map.get(session, :preferences, %{})

    %{
      "id" => Map.get(session, :id),
      "status" => normalize_status(Map.get(session, :status, :waiting)),
      "partner_id" => Map.get(session, :partner_id),
      "last_partner_id" => Map.get(session, :last_partner_id),
      "mode" => normalize_mode(Map.get(preferences, "mode", Map.get(preferences, :mode, "text"))),
      "interest_buckets" =>
        preferences
        |> Map.get("interests", Map.get(preferences, :interests, ""))
        |> interest_buckets()
    }
  end

  defp normalize_status(value) when is_atom(value), do: Atom.to_string(value)
  defp normalize_status(value) when is_binary(value), do: value
  defp normalize_status(_value), do: "waiting"

  defp normalize_mode(mode) when mode in ["lobby", "text", "video"], do: mode
  defp normalize_mode(mode) when is_atom(mode), do: mode |> Atom.to_string() |> normalize_mode()
  defp normalize_mode(_mode), do: "text"

  defp interest_buckets(interests) do
    interests
    |> to_string_value("")
    |> String.slice(0, 500)
    |> String.downcase()
    |> String.split([",", ";"], trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.map(&bucket_name/1)
    |> Enum.uniq()
    |> Enum.take(3)
  end

  defp bucket_name(tag) do
    normalized =
      tag
      |> String.downcase()
      |> String.replace(~r/[^a-z0-9]+/u, "-")
      |> String.trim("-")
      |> String.slice(0, 32)

    if normalized == "", do: "misc", else: normalized
  end

  defp to_string_value(nil, default), do: default
  defp to_string_value(value, _default) when is_binary(value), do: value
  defp to_string_value(value, _default) when is_atom(value), do: Atom.to_string(value)
  defp to_string_value(value, _default) when is_integer(value), do: Integer.to_string(value)

  defp to_string_value(value, _default) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp to_string_value(_value, default), do: default

  defp hashed_token(nil), do: hashed_token("")

  defp hashed_token(token) do
    :crypto.hash(:sha256, token) |> Base.encode16(case: :lower)
  end

  defp active_sessions_key, do: "sessions:active"
  defp session_key(session_id), do: "session:data:#{session_id}"
  defp session_ip_key(session_id), do: "session:#{session_id}:ip"
  defp session_token_key(session_id), do: "session:#{session_id}:token"
  defp queue_meta_key(session_id), do: "#{@queue_meta_key_prefix}:#{session_id}:queue_meta"
  defp session_owner_key(session_id), do: "session:#{session_id}:owner"
  defp ip_sessions_key(ip), do: "ip:#{ip}"
  defp match_key(session_id), do: "match:#{session_id}"
  defp recent_match_key(session_id), do: "recent_match:#{session_id}"
end
