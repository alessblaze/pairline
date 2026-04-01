defmodule OmeglePhoenix.RedisState do
  @moduledoc false

  @persist_script """
  redis.call('SETEX', KEYS[1], ARGV[1], ARGV[5])
  redis.call('SETEX', KEYS[2], ARGV[1], ARGV[3])
  redis.call('SETEX', KEYS[3], ARGV[1], ARGV[4])
  redis.call('SADD', KEYS[4], ARGV[2])
  redis.call('SADD', KEYS[5], ARGV[2])
  if redis.call('EXISTS', KEYS[6]) == 1 then
    redis.call('EXPIRE', KEYS[6], ARGV[1])
  end
  return 1
  """

  @delete_script """
  redis.call('SREM', KEYS[1], ARGV[1])
  redis.call('SREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3], KEYS[4], KEYS[5], KEYS[6], KEYS[7], KEYS[8])
  if redis.call('SCARD', KEYS[2]) == 0 then
    redis.call('DEL', KEYS[2])
  end
  return 1
  """

  @pair_script """
  redis.call('SETEX', KEYS[1], ARGV[1], ARGV[5])
  redis.call('SETEX', KEYS[2], ARGV[1], ARGV[3])
  redis.call('SETEX', KEYS[3], ARGV[1], ARGV[4])
  redis.call('SETEX', KEYS[7], ARGV[1], ARGV[10])
  redis.call('SETEX', KEYS[8], ARGV[1], ARGV[8])
  redis.call('SETEX', KEYS[9], ARGV[1], ARGV[9])
  redis.call('SADD', KEYS[11], ARGV[2], ARGV[7])
  redis.call('SADD', KEYS[12], ARGV[2])
  redis.call('SADD', KEYS[13], ARGV[7])
  if redis.call('EXISTS', KEYS[4]) == 1 then redis.call('EXPIRE', KEYS[4], ARGV[1]) end
  if redis.call('EXISTS', KEYS[10]) == 1 then redis.call('EXPIRE', KEYS[10], ARGV[1]) end
  redis.call('SETEX', KEYS[14], ARGV[1], ARGV[7])
  redis.call('SETEX', KEYS[15], ARGV[1], ARGV[2])
  redis.call('SETEX', KEYS[16], ARGV[6], ARGV[7])
  redis.call('SETEX', KEYS[17], ARGV[6], ARGV[2])
  return 1
  """

  @reset_pair_script """
  redis.call('SETEX', KEYS[1], ARGV[1], ARGV[5])
  redis.call('SETEX', KEYS[2], ARGV[1], ARGV[3])
  redis.call('SETEX', KEYS[3], ARGV[1], ARGV[4])
  redis.call('SETEX', KEYS[7], ARGV[1], ARGV[9])
  redis.call('SETEX', KEYS[8], ARGV[1], ARGV[7])
  redis.call('SETEX', KEYS[9], ARGV[1], ARGV[8])
  redis.call('SADD', KEYS[11], ARGV[2], ARGV[6])
  redis.call('SADD', KEYS[12], ARGV[2])
  redis.call('SADD', KEYS[13], ARGV[6])
  if redis.call('EXISTS', KEYS[4]) == 1 then redis.call('EXPIRE', KEYS[4], ARGV[1]) end
  if redis.call('EXISTS', KEYS[10]) == 1 then redis.call('EXPIRE', KEYS[10], ARGV[1]) end
  redis.call('DEL', KEYS[15], KEYS[16])
  return 1
  """

  def persist_session(session, ttl_seconds, opts \\ []) do
    ttl = Integer.to_string(ttl_seconds)
    hashed_token = hashed_token(session.token)

    command = [
      "EVAL",
      @persist_script,
      "6",
      session_key(session.id),
      session_ip_key(session.id),
      session_token_key(session.id),
      active_sessions_key(),
      ip_sessions_key(session.ip),
      session_owner_key(session.id),
      ttl,
      session.id,
      session.ip,
      hashed_token,
      encode_session(session)
    ]

    exec(command, opts)
  end

  def delete_session(session_id, ip, opts \\ []) do
    command = [
      "EVAL",
      @delete_script,
      "8",
      active_sessions_key(),
      ip_sessions_key(ip),
      session_key(session_id),
      session_ip_key(session_id),
      session_token_key(session_id),
      session_owner_key(session_id),
      match_key(session_id),
      recent_match_key(session_id),
      session_id
    ]

    exec(command, opts)
  end

  def pair_sessions(session1, session2, ttl_seconds, recent_ttl_seconds, opts \\ []) do
    ttl = Integer.to_string(ttl_seconds)
    recent_ttl = Integer.to_string(recent_ttl_seconds)

    command = [
      "EVAL",
      @pair_script,
      "17",
      session_key(session1.id),
      session_ip_key(session1.id),
      session_token_key(session1.id),
      session_owner_key(session1.id),
      match_key(session1.id),
      recent_match_key(session1.id),
      session_key(session2.id),
      session_ip_key(session2.id),
      session_token_key(session2.id),
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
      recent_ttl,
      session2.id,
      session2.ip,
      hashed_token(session2.token),
      encode_session(session2)
    ]

    exec(command, opts)
  end

  def reset_pair(session1, session2, ttl_seconds, opts \\ []) do
    ttl = Integer.to_string(ttl_seconds)

    command = [
      "EVAL",
      @reset_pair_script,
      "16",
      session_key(session1.id),
      session_ip_key(session1.id),
      session_token_key(session1.id),
      session_owner_key(session1.id),
      "dummy5",
      "dummy6",
      session_key(session2.id),
      session_ip_key(session2.id),
      session_token_key(session2.id),
      session_owner_key(session2.id),
      active_sessions_key(),
      ip_sessions_key(session1.ip),
      ip_sessions_key(session2.ip),
      "dummy14",
      match_key(session1.id),
      match_key(session2.id),
      ttl,
      session1.id,
      session1.ip,
      hashed_token(session1.token),
      encode_session(session1),
      session2.id,
      session2.ip,
      hashed_token(session2.token),
      encode_session(session2)
    ]

    exec(command, opts)
  end

  def cleanup_orphaned_session(session_id, ip \\ nil, opts \\ []) do
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

    delete_session(session_id, ip_value, opts)
  end

  defp exec(command, opts) do
    case OmeglePhoenix.Redis.command(command) do
      {:ok, _result} = ok ->
        if Keyword.get(opts, :telemetry, true) do
          :telemetry.execute([:omegle_phoenix, :redis_state, :success], %{count: 1}, %{operation: hd(command)})
        end

        ok

      {:error, reason} = error ->
        if Keyword.get(opts, :telemetry, true) do
          :telemetry.execute([:omegle_phoenix, :redis_state, :failure], %{count: 1}, %{operation: hd(command), reason: inspect(reason)})
        end

        error
    end
  end

  defp encode_session(session), do: session |> serialize_session() |> Jason.encode!()

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

  defp hashed_token(token) do
    :crypto.hash(:sha256, token) |> Base.encode16(case: :lower)
  end

  defp active_sessions_key, do: "sessions:active"
  defp session_key(session_id), do: "session:data:#{session_id}"
  defp session_ip_key(session_id), do: "session:#{session_id}:ip"
  defp session_token_key(session_id), do: "session:#{session_id}:token"
  defp session_owner_key(session_id), do: "session:#{session_id}:owner_node"
  defp ip_sessions_key(ip), do: "ip:#{ip}"
  defp match_key(session_id), do: "match:#{session_id}"
  defp recent_match_key(session_id), do: "recent_match:#{session_id}"
end
