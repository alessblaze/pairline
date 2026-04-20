defmodule OmeglePhoenix.Bots.ScriptWorker do
  @moduledoc false

  use GenServer
  require OpenTelemetry.Tracer, as: Tracer

  alias OmeglePhoenix.Tracing

  @initial_reply_delay_ms 500

  def start_link(opts) do
    GenServer.start_link(__MODULE__, opts)
  end

  @impl true
  def init(opts) do
    session = Keyword.fetch!(opts, :session)
    definition = Keyword.fetch!(opts, :definition)
    bot_session_id = Uniq.UUID.uuid4()

    mode = session.preferences |> Map.get("mode", "text")
    preferences = %{"mode" => mode, "interests" => ""}

    with {:ok, _bot_session} <-
           OmeglePhoenix.SessionManager.create_bot_session(bot_session_id, preferences),
         :ok <- OmeglePhoenix.Router.register(bot_session_id, self()),
         :ok <- pair_with_human(session.id, bot_session_id) do
      {:ok,
       %{
         human_session_id: session.id,
         bot_session_id: bot_session_id,
         definition: definition,
         match_generation: nil,
         bot_messages_sent: 0,
         max_messages: max_messages(definition),
         delivery_in_flight: false,
         delivery_timer: nil,
         queued_replies: []
       }}
    else
      _error ->
        _ = OmeglePhoenix.Router.unregister(bot_session_id)
        _ = OmeglePhoenix.SessionManager.delete_session(bot_session_id)
        :ignore
    end
  end

  @impl true
  def handle_info(
        {:router_match, partner_session_id, _common_interests, match_generation, _route, _owner,
         _partner_meta},
        state
      ) do
    case OmeglePhoenix.SessionManager.get_session(partner_session_id) do
      {:ok, %{session_kind: :bot} = partner_session} ->
        _ =
          OmeglePhoenix.Bots.disconnect_bot_collision(
            %{id: state.bot_session_id},
            partner_session
          )

        {:stop, :normal, state}

      _ ->
        Process.send_after(self(), :send_opening_message, @initial_reply_delay_ms)

        {:noreply,
         %{state | human_session_id: partner_session_id, match_generation: match_generation}}
    end
  end

  def handle_info(:send_opening_message, state) do
    state = enqueue_reply(state, opening_message(state.definition), :opening)
    {:noreply, state}
  end

  def handle_info(
        {:router_message,
         %{type: "message", from: from, match_generation: generation, data: data}},
        state
      ) do
    cond do
      from != state.human_session_id or generation != state.match_generation ->
        {:noreply, state}

      true ->
        content = data |> Map.get(:content) || Map.get(data, "content") || ""

        state =
          Tracer.with_span "bots.script_worker.enqueue_reply", %{kind: :internal} do
            Tracing.annotate_internal("bots.script_worker.enqueue_reply", %{
              "session.ref" => Tracing.safe_ref(state.human_session_id),
              "bot.ref" => Tracing.safe_ref(state.bot_session_id),
              "bot.type" => "engagement"
            })

            enqueue_user_reply(state, to_string(content))
          end

        {:noreply, state}
    end
  end

  def handle_info({:deliver_message, content}, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :reply_sent],
      %{count: 1},
      %{bot_type: "engagement"}
    )

    # 1. Send typing = false
    OmeglePhoenix.Router.send_message(
      state.human_session_id,
      %{
        type: "typing",
        from: state.bot_session_id,
        match_generation: state.match_generation,
        data: %{typing: false}
      }
    )

    # 2. Deliver actual message
    OmeglePhoenix.Router.send_message(
      state.human_session_id,
      %{
        type: "message",
        from: state.bot_session_id,
        match_generation: state.match_generation,
        data: %{content: content}
      }
    )

    next_state =
      state
      |> Map.put(:delivery_in_flight, false)
      |> Map.put(:delivery_timer, nil)
      |> Map.put(:bot_messages_sent, state.bot_messages_sent + 1)

    {:noreply, maybe_start_next_delivery(next_state)}
  end

  def handle_info(:disconnect_bot, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "engagement", reason: "bot_finished"}
    )

    disconnect_human_partner(state, "bot finished")
    {:stop, :normal, state}
  end

  def handle_info({:router_disconnect, _reason, _generation}, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "engagement", reason: "partner_disconnect"}
    )

    {:stop, :normal, state}
  end

  def handle_info(:router_timeout, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "engagement", reason: "router_timeout"}
    )

    {:stop, :normal, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    cancel_timer(state[:delivery_timer])

    if definition = state[:definition] do
      _ = OmeglePhoenix.Bots.release_definition_slot(definition)
    end

    if bot_session_id = state[:bot_session_id] do
      _ = OmeglePhoenix.Router.unregister(bot_session_id)
      _ = OmeglePhoenix.Matchmaker.leave_queue(bot_session_id)
      _ = OmeglePhoenix.SessionManager.delete_session(bot_session_id)
    end

    :ok
  end

  defp pair_with_human(human_session_id, bot_session_id) do
    Tracer.with_span "bots.script_worker.pair_with_human", %{kind: :internal} do
      Tracing.annotate_internal("bots.script_worker.pair_with_human", %{
        "session.ref" => Tracing.safe_ref(human_session_id),
        "bot.ref" => Tracing.safe_ref(bot_session_id),
        "bot.type" => "engagement"
      })

      OmeglePhoenix.SessionLock.with_locks([human_session_id, bot_session_id], fn ->
        with {:ok, human_session} <- OmeglePhoenix.SessionManager.get_session(human_session_id),
             {:ok, bot_session} <- OmeglePhoenix.SessionManager.get_session(bot_session_id),
             true <- human_session.status == :waiting and is_nil(human_session.partner_id),
             true <- bot_session.status == :waiting and is_nil(bot_session.partner_id),
             {:ok, updated_human, updated_bot, common_interests} <-
               OmeglePhoenix.SessionManager.pair_sessions(human_session, bot_session) do
          _ = OmeglePhoenix.Matchmaker.leave_queue(human_session_id)

          bot_route = %{
            mode: OmeglePhoenix.RedisKeys.mode(updated_bot.preferences),
            shard: updated_bot.redis_shard
          }

          human_route = %{
            mode: OmeglePhoenix.RedisKeys.mode(updated_human.preferences),
            shard: updated_human.redis_shard
          }

          owner_node_human = owner_node(updated_human.id, human_route)
          owner_node_bot = owner_node(updated_bot.id, bot_route)
          generation = updated_human.match_generation || updated_bot.match_generation

          OmeglePhoenix.Router.notify_match(
            updated_human.id,
            updated_bot.id,
            common_interests,
            generation,
            bot_route,
            owner_node_bot,
            %{
              session_kind: "bot",
              bot_type: "engagement",
              reportable: true,
              video_enabled: false
            }
          )

          OmeglePhoenix.Router.notify_match(
            updated_bot.id,
            updated_human.id,
            common_interests,
            generation,
            human_route,
            owner_node_human,
            %{
              session_kind: "human",
              reportable: true,
              video_enabled: false
            }
          )

          :ok
        else
          _ -> {:error, :pair_failed}
        end
      end)
    end
  end

  defp owner_node(session_id, route) do
    case OmeglePhoenix.Router.owner_node(session_id, route_hint: route) do
      {:ok, node_name} -> node_name
      _ -> nil
    end
  end

  defp opening_message(definition) do
    definition
    |> Map.get("script_json", %{})
    |> Map.get("opening_messages", [])
    |> first_or_nil()
  end

  defp next_reply(definition, bot_messages_sent, user_content) do
    script = Map.get(definition, "script_json", %{})
    user_content_str = to_string(user_content)

    cond do
      bot_messages_sent + 1 >= max_messages(definition) ->
        Map.get(script, "closing_message") || Map.get(script, "fallback_message")

      String.trim(user_content_str) == "" ->
        Map.get(script, "fallback_message")

      true ->
        case match_trigger(script, user_content_str) do
          nil ->
            script
            |> Map.get("reply_messages", [])
            |> next_from_list(bot_messages_sent)

          reply ->
            reply
        end
    end
  end

  defp match_trigger(%{"triggers" => triggers}, text) when is_list(triggers) do
    Enum.find_value(triggers, fn
      %{"regex" => regex_str, "reply" => reply} when is_binary(regex_str) and is_binary(reply) ->
        case Regex.compile(regex_str, "i") do
          {:ok, regex} ->
            if Regex.match?(regex, text), do: reply, else: nil

          _ ->
            nil
        end

      _ ->
        nil
    end)
  end

  defp match_trigger(_script, _text), do: nil

  defp enqueue_user_reply(state, content) do
    trimmed = String.trim(content)

    cond do
      trimmed == "" ->
        state

      reply_slots_reserved(state) >= state.max_messages ->
        schedule_disconnect(state, 250)

      true ->
        reply =
          next_reply(
            state.definition,
            reply_slots_reserved(state),
            trimmed
          )

        enqueue_reply(state, reply, :user)
    end
  end

  defp enqueue_reply(state, nil, _source), do: state

  defp enqueue_reply(state, content, source) when is_binary(content) do
    trimmed = String.trim(content)

    cond do
      trimmed == "" ->
        state

      reply_slots_reserved(state) >= state.max_messages ->
        schedule_disconnect(state, 250)

      true ->
        :telemetry.execute(
          [:omegle_phoenix, :bots, :message_enqueued],
          %{count: 1},
          %{bot_type: "engagement", source: Atom.to_string(source)}
        )

        state
        |> update_in([:queued_replies], &(&1 ++ [trimmed]))
        |> maybe_start_next_delivery()
    end
  end

  defp maybe_start_next_delivery(%{delivery_in_flight: true} = state), do: state

  defp maybe_start_next_delivery(%{queued_replies: []} = state) do
    if state.bot_messages_sent >= state.max_messages do
      schedule_disconnect(state, 1_500)
    else
      state
    end
  end

  defp maybe_start_next_delivery(%{queued_replies: [content | rest]} = state) do
    OmeglePhoenix.Router.send_message(
      state.human_session_id,
      %{
        type: "typing",
        from: state.bot_session_id,
        match_generation: state.match_generation,
        data: %{typing: true}
      }
    )

    # Rough typing delay: ~40ms per character, plus 500ms base delay
    # Cap between 800ms and 4000ms to stay responsive
    delay_ms =
      content |> String.length() |> Kernel.*(40) |> Kernel.+(500) |> min(4000) |> max(800)

    timer = Process.send_after(self(), {:deliver_message, content}, delay_ms)

    %{state | queued_replies: rest, delivery_in_flight: true, delivery_timer: timer}
  end

  defp reply_slots_reserved(state) do
    state.bot_messages_sent + length(state.queued_replies) +
      if(state.delivery_in_flight, do: 1, else: 0)
  end

  defp schedule_disconnect(state, delay_ms) do
    Process.send_after(self(), :disconnect_bot, delay_ms)
    state
  end

  defp cancel_timer(nil), do: :ok
  defp cancel_timer(timer_ref), do: Process.cancel_timer(timer_ref, async: false, info: false)

  defp disconnect_human_partner(state, reason) do
    with {:ok, bot_session} <- OmeglePhoenix.SessionManager.get_session(state.bot_session_id),
         {:ok, human_session} <- OmeglePhoenix.SessionManager.get_session(state.human_session_id),
         {:ok, _updated_bot, updated_human} <-
           OmeglePhoenix.SessionManager.reset_pair(bot_session, human_session) do
      OmeglePhoenix.Router.notify_disconnect(
        updated_human.id,
        reason,
        updated_human.match_generation
      )
    else
      _ ->
        OmeglePhoenix.Router.notify_disconnect(
          state.human_session_id,
          reason,
          state.match_generation
        )
    end
  end

  defp max_messages(definition) do
    definition
    |> Map.get("message_limit", 4)
    |> case do
      value when is_integer(value) and value > 0 -> value
      _ -> 4
    end
  end

  defp first_or_nil([value | _]) when is_binary(value), do: value
  defp first_or_nil(_), do: nil

  defp next_from_list(messages, index) when is_list(messages) do
    case Enum.filter(messages, &is_binary/1) do
      [] -> nil
      filtered -> Enum.at(filtered, rem(max(index, 0), length(filtered)))
    end
  end

  defp next_from_list(_messages, _index), do: nil
end
