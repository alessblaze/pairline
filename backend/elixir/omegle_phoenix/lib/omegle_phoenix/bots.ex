defmodule OmeglePhoenix.Bots do
  @moduledoc false

  alias OmeglePhoenix.Bots.ScriptWorker

  @snapshot_key "bots:definitions:snapshot"
  @active_run_key_prefix "bots:engagement:active_runs:"
  @reserve_slot_script """
  local current = tonumber(redis.call('GET', KEYS[1]) or '0')
  local limit = tonumber(ARGV[1] or '0')
  local ttl = tonumber(ARGV[2] or '0')

  if limit <= 0 then
    return 0
  end

  if current >= limit then
    return 0
  end

  current = redis.call('INCR', KEYS[1])

  if current == 1 and ttl > 0 then
    redis.call('EXPIRE', KEYS[1], ttl)
  end

  if current > limit then
    redis.call('DECR', KEYS[1])
    return 0
  end

  return current
  """
  @release_slot_script """
  local current = tonumber(redis.call('GET', KEYS[1]) or '0')

  if current <= 1 then
    redis.call('DEL', KEYS[1])
    return 0
  end

  return redis.call('DECR', KEYS[1])
  """

  def maybe_assign_waiting_session(session) when is_map(session) do
    require Logger

    if session[:session_kind] == :bot do
      :skip
    else
      snapshot_result = load_snapshot()

      case snapshot_result do
        {:ok, snapshot} ->
          enabled = bots_enabled?(snapshot)
          rollout = rollout_allows?(snapshot, session.id)
          defs = matching_definitions(snapshot, session)

          Logger.debug(
            "Bots.maybe_assign: enabled=#{inspect(enabled)} rollout=#{inspect(rollout)} " <>
              "defs_count=#{if is_list(defs), do: length(defs), else: inspect(defs)} " <>
              "rollout_percent=#{inspect(get_in(snapshot, ["settings", "rollout_percent"]))} " <>
              "session_id=#{inspect(session.id)}"
          )

          if enabled and rollout and is_list(defs) and defs != [] do
            case try_start_worker(defs, session) do
              {:ok, _definition, _pid} ->
                Logger.info("Bots.maybe_assign: matched session #{inspect(session.id)} to bot")
                :matched

              other ->
                Logger.debug("Bots.maybe_assign: try_start_worker failed: #{inspect(other)}")
                :skip
            end
          else
            :skip
          end

        other ->
          Logger.debug("Bots.maybe_assign: snapshot load failed: #{inspect(other)}")
          :skip
      end
    end
  end

  def disconnect_bot_collision(session1, session2) do
    Enum.each([session1, session2], fn session ->
      _ = OmeglePhoenix.Matchmaker.leave_queue(session.id)
      _ = OmeglePhoenix.Router.notify_disconnect(session.id, "bot collision")
      _ = OmeglePhoenix.SessionManager.delete_session(session.id)
    end)

    :ok
  end

  def load_snapshot do
    case OmeglePhoenix.Redis.command(["GET", @snapshot_key]) do
      {:ok, nil} ->
        {:error, :not_found}

      {:ok, payload} when is_binary(payload) ->
        JSON.decode(payload)

      {:error, reason} ->
        {:error, reason}

      other ->
        {:error, {:unexpected_snapshot_result, other}}
    end
  end

  defp bots_enabled?(%{"settings" => settings}) when is_map(settings) do
    Map.get(settings, "enabled", true) and
      Map.get(settings, "engagement_enabled", true) and
      not Map.get(settings, "emergency_stop", false)
  end

  defp bots_enabled?(_snapshot), do: false

  defp rollout_allows?(%{"settings" => settings}, session_id) when is_map(settings) do
    rollout_percent = Map.get(settings, "rollout_percent", 0)

    cond do
      rollout_percent >= 100 -> true
      rollout_percent <= 0 -> false
      true -> rem(:erlang.phash2(session_id, 10_000), 100) < rollout_percent
    end
  end

  defp rollout_allows?(_snapshot, _session_id), do: false

  def release_definition_slot(definition) do
    definition
    |> definition_id()
    |> case do
      nil ->
        :ok

      definition_id ->
        case OmeglePhoenix.Redis.command([
               "EVAL",
               @release_slot_script,
               "1",
               active_run_key(definition_id)
             ]) do
          {:ok, _value} -> :ok
          {:error, reason} -> {:error, reason}
          _other -> :ok
        end
    end
  end

  defp matching_definitions(%{"definitions" => definitions}, session)
       when is_list(definitions) and is_map(session) do
    mode = session.preferences |> Map.get("mode", "text")

    definitions
    |> Enum.filter(fn
      %{"bot_type" => "engagement", "is_active" => true} = definition ->
        mode_allowed?(definition, mode) and definition_bot_count(definition) > 0

      _ ->
        false
    end)
    |> Enum.sort_by(&definition_priority/1, :desc)
  end

  defp matching_definitions(_snapshot, _session), do: []

  defp mode_allowed?(%{"match_modes_json" => match_modes}, mode) when is_list(match_modes) do
    Enum.any?(match_modes, &(&1 == mode))
  end

  defp mode_allowed?(_definition, _mode), do: false

  defp try_start_worker([], _session), do: {:error, :no_matching_definition}

  defp try_start_worker([definition | rest], session) do
    case reserve_definition_slot(definition) do
      :ok ->
        child_spec = {ScriptWorker, session: session, definition: definition}

        case DynamicSupervisor.start_child(OmeglePhoenix.Bots.Supervisor, child_spec) do
          {:ok, pid} -> {:ok, definition, pid}
          {:error, {:already_started, pid}} -> {:ok, definition, pid}
          {:error, _reason} -> release_and_continue(definition, rest, session)
          :ignore -> release_and_continue(definition, rest, session)
        end

      :full ->
        try_start_worker(rest, session)

      {:error, _reason} ->
        try_start_worker(rest, session)
    end
  end

  defp release_and_continue(definition, rest, session) do
    _ = release_definition_slot(definition)
    try_start_worker(rest, session)
  end

  defp reserve_definition_slot(definition) do
    case definition_id(definition) do
      nil ->
        {:error, :missing_definition_id}

      definition_id ->
        result = OmeglePhoenix.Redis.command([
               "EVAL",
               @reserve_slot_script,
               "1",
               active_run_key(definition_id),
               Integer.to_string(definition_bot_count(definition)),
               Integer.to_string(slot_ttl_seconds(definition))
             ])

        case result do
          {:ok, count} when is_integer(count) and count > 0 -> :ok
          {:ok, count} when is_binary(count) ->
            case Integer.parse(count) do
              {n, ""} when n > 0 -> :ok
              _ -> :full
            end
          {:ok, 0} -> :full
          {:error, reason} -> {:error, reason}
          _other -> :full
        end
    end
  end

  defp active_run_key(definition_id), do: @active_run_key_prefix <> definition_id

  defp definition_priority(definition) do
    {definition_traffic_weight(definition), definition_bot_count(definition)}
  end

  defp definition_traffic_weight(definition) do
    definition
    |> Map.get("traffic_weight", 100)
    |> normalize_positive_integer(100)
  end

  defp definition_bot_count(definition) do
    definition
    |> Map.get("bot_count", 1)
    |> normalize_positive_integer(1)
  end

  defp slot_ttl_seconds(definition) do
    ttl =
      definition
      |> Map.get("session_ttl_seconds", 300)
      |> normalize_positive_integer(300)

    max(ttl + 60, 120)
  end

  defp definition_id(definition) when is_map(definition) do
    case Map.get(definition, "id") do
      value when is_binary(value) and value != "" -> value
      _ -> nil
    end
  end

  defp normalize_positive_integer(value, _default) when is_integer(value) and value > 0, do: value

  defp normalize_positive_integer(value, default) when is_binary(value) do
    case Integer.parse(value) do
      {parsed, ""} when parsed > 0 -> parsed
      _ -> default
    end
  end

  defp normalize_positive_integer(_value, default), do: default
end
