defmodule OmeglePhoenix.Bots do
  @moduledoc false

  require OpenTelemetry.Tracer, as: Tracer

  alias OmeglePhoenix.Bots.AIWorker
  alias OmeglePhoenix.Bots.ScriptWorker
  alias OmeglePhoenix.Tracing

  @snapshot_key "bots:definitions:snapshot"
  # Keep both keys in the same Redis Cluster hash slot so the Lua scripts can
  # atomically reserve and release shared/global capacity.
  @active_run_hash_tag "{bot-active-runs}"
  @active_run_key_prefix "bots:active_runs:" <> @active_run_hash_tag <> ":definition:"
  @global_active_run_key "bots:active_runs:" <> @active_run_hash_tag <> ":global"
  @reserve_slot_script """
  local global_current = tonumber(redis.call('GET', KEYS[1]) or '0')
  local definition_current = tonumber(redis.call('GET', KEYS[2]) or '0')
  local global_limit = tonumber(ARGV[1] or '0')
  local definition_limit = tonumber(ARGV[2] or '0')
  local ttl = tonumber(ARGV[3] or '0')

  if definition_limit <= 0 then
    return -1
  end

  if global_limit <= 0 then
    return -2
  end

  if definition_current >= definition_limit then
    return -1
  end

  if global_current >= global_limit then
    return -2
  end

  global_current = redis.call('INCR', KEYS[1])

  if global_current == 1 and ttl > 0 then
    redis.call('EXPIRE', KEYS[1], ttl)
  end

  if global_current > global_limit then
    redis.call('DECR', KEYS[1])
    return -2
  end

  definition_current = redis.call('INCR', KEYS[2])

  if definition_current == 1 and ttl > 0 then
    redis.call('EXPIRE', KEYS[2], ttl)
  end

  if definition_current > definition_limit then
    redis.call('DECR', KEYS[2])
    redis.call('DECR', KEYS[1])
    return -1
  end

  return definition_current
  """
  @release_slot_script """
  local global_current = tonumber(redis.call('GET', KEYS[1]) or '0')
  local definition_current = tonumber(redis.call('GET', KEYS[2]) or '0')

  if global_current <= 1 then
    redis.call('DEL', KEYS[1])
  else
    redis.call('DECR', KEYS[1])
  end

  if definition_current <= 1 then
    redis.call('DEL', KEYS[2])
    return 0
  end

  return redis.call('DECR', KEYS[2])
  """

  def maybe_assign_waiting_session(session) when is_map(session) do
    require Logger

    Tracer.with_span "bots.maybe_assign_waiting_session", %{kind: :internal} do
      Tracing.annotate_internal("bots.maybe_assign_waiting_session", %{
        "session.ref" => Tracing.safe_ref(session[:id])
      })

      if session[:session_kind] == :bot do
        :skip
      else
        snapshot_result = load_snapshot()

        case snapshot_result do
          {:ok, snapshot} ->
            enabled = bots_enabled?(snapshot)
            rollout = rollout_allows?(snapshot, session.id)
            defs = matching_definitions(snapshot, session)
            settings = Map.get(snapshot, "settings", %{})

            Logger.debug(
              "Bots.maybe_assign: enabled=#{inspect(enabled)} rollout=#{inspect(rollout)} " <>
                "defs_count=#{if is_list(defs), do: length(defs), else: inspect(defs)} " <>
                "rollout_percent=#{inspect(get_in(snapshot, ["settings", "rollout_percent"]))} " <>
                "session_id=#{inspect(session.id)}"
            )

            if enabled and rollout and is_list(defs) and defs != [] do
              case try_start_worker(defs, session, settings) do
                {:ok, definition, _pid} ->
                  Logger.info("Bots.maybe_assign: matched session #{inspect(session.id)} to bot")

                  :telemetry.execute(
                    [:omegle_phoenix, :bots, :worker_started],
                    %{count: 1},
                    %{
                      bot_type: Map.get(definition, "bot_type", "engagement")
                    }
                  )

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
    Map.get(settings, "enabled", true) and not Map.get(settings, "emergency_stop", false)
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
               "2",
               @global_active_run_key,
               active_run_key(definition_id)
             ]) do
          {:ok, _value} -> :ok
          {:error, reason} -> {:error, reason}
          _other -> :ok
        end
    end
  end

  defp matching_definitions(%{"definitions" => definitions, "settings" => settings}, session)
       when is_list(definitions) and is_map(session) and is_map(settings) do
    mode = session.preferences |> Map.get("mode", "text")
    engagement_enabled = Map.get(settings, "engagement_enabled", true)
    ai_enabled = Map.get(settings, "ai_enabled", true)

    definitions
    |> Enum.filter(fn
      %{"bot_type" => "engagement", "is_active" => true} = definition ->
        engagement_enabled and mode_allowed?(definition, mode) and
          definition_bot_count(definition) > 0

      %{"bot_type" => "ai", "is_active" => true} = definition ->
        ai_enabled and mode_allowed?(definition, mode) and ai_definition_configured?(definition)

      _ ->
        false
    end)
    |> prioritize_definitions(settings)
  end

  defp matching_definitions(_snapshot, _session), do: []

  defp mode_allowed?(%{"match_modes_json" => match_modes}, mode) when is_list(match_modes) do
    Enum.any?(match_modes, &(&1 == mode))
  end

  defp mode_allowed?(_definition, _mode), do: false

  def reserve_definition_slot(definition, settings \\ %{}) do
    case definition_id(definition) do
      nil ->
        {:error, :missing_definition_id}

      definition_id ->
        result =
          OmeglePhoenix.Redis.command([
            "EVAL",
            @reserve_slot_script,
            "2",
            @global_active_run_key,
            active_run_key(definition_id),
            Integer.to_string(max_concurrent_runs(settings)),
            Integer.to_string(definition_bot_count(definition)),
            Integer.to_string(slot_ttl_seconds(definition))
          ])

        case result do
          {:ok, count} when is_integer(count) and count > 0 ->
            :ok

          {:ok, count} when is_binary(count) ->
            case Integer.parse(count) do
              {n, ""} when n > 0 -> :ok
              {-1, ""} -> :definition_full
              {-2, ""} -> :global_full
              _ -> :definition_full
            end

          {:ok, -1} ->
            :definition_full

          {:ok, -2} ->
            :global_full

          {:ok, 0} ->
            :definition_full

          {:error, reason} ->
            {:error, reason}

          _other ->
            :definition_full
        end
    end
  end

  defp try_start_worker([], _session, _settings), do: {:error, :no_matching_definition}

  defp try_start_worker([definition | rest], session, settings) do
    case reserve_definition_slot(definition, settings) do
      :ok ->
        child_spec = worker_child_spec(definition, session)

        case DynamicSupervisor.start_child(OmeglePhoenix.Bots.Supervisor, child_spec) do
          {:ok, pid} -> {:ok, definition, pid}
          {:error, {:already_started, pid}} -> {:ok, definition, pid}
          {:error, _reason} -> release_and_continue(definition, rest, session, settings)
          :ignore -> release_and_continue(definition, rest, session, settings)
        end

      :definition_full ->
        try_start_worker(rest, session, settings)

      :global_full ->
        {:error, :global_capacity_reached}

      {:error, _reason} ->
        try_start_worker(rest, session, settings)
    end
  end

  defp release_and_continue(definition, rest, session, settings) do
    _ = release_definition_slot(definition)
    try_start_worker(rest, session, settings)
  end

  defp active_run_key(definition_id), do: @active_run_key_prefix <> definition_id

  def prioritize_definitions(definitions, settings) when is_list(definitions) do
    definitions
    |> Enum.group_by(&bot_type_priority(&1, settings))
    |> Enum.sort_by(fn {priority, _definitions} -> priority end, :desc)
    |> Enum.flat_map(fn {_priority, grouped_definitions} ->
      weighted_shuffle(grouped_definitions)
    end)
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

  defp bot_type_priority(%{"bot_type" => "engagement"}, settings) when is_map(settings) do
    settings
    |> Map.get("engagement_priority", 100)
    |> normalize_non_negative_integer(100)
  end

  defp bot_type_priority(%{"bot_type" => "ai"}, settings) when is_map(settings) do
    settings
    |> Map.get("ai_priority", 100)
    |> normalize_non_negative_integer(100)
  end

  defp bot_type_priority(_definition, _settings), do: 100

  defp max_concurrent_runs(settings) when is_map(settings) do
    settings
    |> Map.get("max_concurrent_runs", 100)
    |> normalize_positive_integer(100)
  end

  defp max_concurrent_runs(_settings), do: 100

  defp definition_capacity(definition), do: definition_bot_count(definition)

  defp weighted_shuffle(definitions) do
    definitions
    |> Enum.map(fn definition ->
      {weighted_selection_key(definition), definition_capacity(definition), definition}
    end)
    |> Enum.sort_by(
      fn {selection_key, capacity, _definition} -> {selection_key, capacity} end,
      :desc
    )
    |> Enum.map(fn {_selection_key, _capacity, definition} -> definition end)
  end

  # Weighted random ordering without replacement: larger traffic_weight values
  # are more likely to appear earlier, while still allowing lower-weight bots
  # to receive some traffic within the same priority tier.
  defp weighted_selection_key(definition) do
    weight = definition_traffic_weight(definition)
    uniform = max(:rand.uniform(), 1.0e-12)
    :math.pow(uniform, 1.0 / weight)
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

  defp ai_definition_configured?(%{"ai_config_json" => ai_config}) when is_map(ai_config) do
    enabled = Map.get(ai_config, "enabled", true)
    endpoint = Map.get(ai_config, "api_url") || Map.get(ai_config, "endpoint")
    token = Map.get(ai_config, "api_token") || Map.get(ai_config, "api_key")
    model = Map.get(ai_config, "model")

    enabled and is_binary(endpoint) and endpoint != "" and is_binary(token) and token != "" and
      is_binary(model) and model != ""
  end

  defp ai_definition_configured?(_definition), do: false

  defp worker_child_spec(%{"bot_type" => "ai"} = definition, session) do
    {AIWorker, session: session, definition: definition}
  end

  defp worker_child_spec(definition, session) do
    {ScriptWorker, session: session, definition: definition}
  end

  defp normalize_positive_integer(value, _default) when is_integer(value) and value > 0, do: value

  defp normalize_positive_integer(value, default) when is_binary(value) do
    case Integer.parse(value) do
      {parsed, ""} when parsed > 0 -> parsed
      _ -> default
    end
  end

  defp normalize_positive_integer(_value, default), do: default

  defp normalize_non_negative_integer(value, _default)
       when is_integer(value) and value >= 0,
       do: value

  defp normalize_non_negative_integer(value, default) when is_binary(value) do
    case Integer.parse(value) do
      {parsed, ""} when parsed >= 0 -> parsed
      _ -> default
    end
  end

  defp normalize_non_negative_integer(_value, default), do: default
end
