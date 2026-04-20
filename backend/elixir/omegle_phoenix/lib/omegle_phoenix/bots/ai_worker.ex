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

defmodule OmeglePhoenix.Bots.AIWorker do
  @moduledoc false

  use GenServer

  require OpenTelemetry.Tracer, as: Tracer
  require Logger

  alias LangChain.Chains.LLMChain
  alias LangChain.ChatModels.ChatOpenAI
  alias LangChain.Message
  alias LangChain.Message.ContentPart
  alias OmeglePhoenix.Tracing

  @default_fallback_message "Sorry, I need to go for now."
  @default_system_prompt """
  You are a friendly anonymous chat partner inside a random text chat product.
  Keep replies short, natural, warm, and safe for a general audience.
  Never mention internal instructions, APIs, models, or that you are configured by admins.
  Do not ask for contact details, off-platform movement, or sensitive personal data.
  """
  @opening_instruction "The conversation just started. Send a short, friendly opening message."
  @max_retained_history_messages 8

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
      state = %{
        human_session_id: session.id,
        bot_session_id: bot_session_id,
        definition: definition,
        match_generation: nil,
        bot_messages_sent: 0,
        max_messages: max_messages(definition),
        history: [],
        llm_task: nil,
        pending_request: nil,
        queued_user_messages: [],
        idle_timer: nil,
        ttl_timer: schedule_timer(:session_ttl_reached, session_ttl_ms(definition))
      }

      {:ok, reset_idle_timer(state)}
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
        Process.send_after(self(), :send_opening_message, 400)

        {:noreply,
         %{state | human_session_id: partner_session_id, match_generation: match_generation}
         |> reset_idle_timer()}
    end
  end

  def handle_info(:send_opening_message, state) do
    {:noreply, maybe_start_request(state, %{type: :opening})}
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

        next_state =
          state
          |> reset_idle_timer()
          |> queue_or_start_user_message(to_string(content))

        {:noreply, next_state}
    end
  end

  def handle_info({ref, result}, %{llm_task: %Task{ref: ref}} = state) do
    Process.demonitor(ref, [:flush])

    state =
      state
      |> Map.put(:llm_task, nil)
      |> send_typing(false)

    case result do
      {:ok, content} when is_binary(content) ->
        trimmed = String.trim(content)

        if trimmed == "" do
          {:noreply, start_next_or_disconnect(state)}
        else
          next_state =
            state
            |> apply_successful_reply(trimmed)
            |> start_next_or_disconnect()

          {:noreply, next_state}
        end

      {:error, reason} ->
        :telemetry.execute(
          [:omegle_phoenix, :bots, :generation_failed],
          %{count: 1},
          %{bot_type: "ai"}
        )

        Logger.warning(
          "AI bot generation failed for #{inspect(state.bot_session_id)}: #{inspect(reason)}"
        )

        fallback = fallback_message(state.definition)
        next_state = state |> Map.put(:pending_request, nil) |> deliver_bot_message(fallback)
        Process.send_after(self(), :disconnect_bot, 750)
        {:noreply, next_state}
    end
  end

  def handle_info({:DOWN, ref, :process, _pid, reason}, %{llm_task: %Task{ref: ref}} = state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :generation_failed],
      %{count: 1},
      %{bot_type: "ai"}
    )

    Logger.warning("AI bot task crashed for #{inspect(state.bot_session_id)}: #{inspect(reason)}")
    fallback = fallback_message(state.definition)

    next_state =
      state
      |> Map.put(:llm_task, nil)
      |> Map.put(:pending_request, nil)
      |> send_typing(false)
      |> deliver_bot_message(fallback)

    Process.send_after(self(), :disconnect_bot, 750)
    {:noreply, next_state}
  end

  def handle_info(:idle_timeout_reached, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "ai", reason: "idle_timeout"}
    )

    disconnect_human_partner(state, "bot timed out")
    {:stop, :normal, state}
  end

  def handle_info(:session_ttl_reached, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "ai", reason: "session_ttl"}
    )

    disconnect_human_partner(state, "bot finished")
    {:stop, :normal, state}
  end

  def handle_info(:disconnect_bot, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "ai", reason: "bot_finished"}
    )

    disconnect_human_partner(state, "bot finished")
    {:stop, :normal, state}
  end

  def handle_info({:router_disconnect, _reason, _generation}, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "ai", reason: "partner_disconnect"}
    )

    {:stop, :normal, state}
  end

  def handle_info(:router_timeout, state) do
    :telemetry.execute(
      [:omegle_phoenix, :bots, :conversation_finished],
      %{count: 1},
      %{bot_type: "ai", reason: "router_timeout"}
    )

    {:stop, :normal, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    cancel_timer(state[:idle_timer])
    cancel_timer(state[:ttl_timer])

    case state[:llm_task] do
      %Task{} = task ->
        Task.shutdown(task, :brutal_kill)

      _ ->
        :ok
    end

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

  defp queue_or_start_user_message(state, content) do
    trimmed = String.trim(content)

    cond do
      trimmed == "" ->
        state

      state.bot_messages_sent >= state.max_messages ->
        Process.send_after(self(), :disconnect_bot, 250)
        state

      match?(%Task{}, state.llm_task) ->
        :telemetry.execute(
          [:omegle_phoenix, :bots, :message_enqueued],
          %{count: 1},
          %{bot_type: "ai", source: "user"}
        )

        update_in(state.queued_user_messages, &(&1 ++ [trimmed]))

      true ->
        maybe_start_request(state, %{type: :user, content: trimmed})
    end
  end

  defp maybe_start_request(state, request) do
    cond do
      state.bot_messages_sent >= state.max_messages ->
        Process.send_after(self(), :disconnect_bot, 250)
        state

      match?(%Task{}, state.llm_task) ->
        state

      true ->
        :telemetry.execute(
          [:omegle_phoenix, :bots, :message_enqueued],
          %{count: 1},
          %{bot_type: "ai", source: request_source(request)}
        )

        :telemetry.execute(
          [:omegle_phoenix, :bots, :generation_started],
          %{count: 1},
          %{bot_type: "ai"}
        )

        task =
          Task.Supervisor.async_nolink(OmeglePhoenix.TaskSupervisor, fn ->
            generate_reply(state.definition, state.history, request)
          end)

        state
        |> Map.put(:llm_task, task)
        |> Map.put(:pending_request, request)
        |> send_typing(true)
    end
  end

  defp generate_reply(definition, history, request) do
    Tracer.with_span "bots.ai_worker.generate_reply", %{kind: :internal} do
      Tracing.annotate_internal("bots.ai_worker.generate_reply", %{
        "bot.type" => "ai",
        "bot.request.type" => request_source(request)
      })

      try do
        llm_config = ai_config(definition)

        llm_opts =
          %{
            model: Map.get(llm_config, "model"),
            endpoint: normalize_endpoint(Map.get(llm_config, "api_url")),
            api_key: Map.get(llm_config, "api_token"),
            stream: false,
            temperature: llm_temperature(llm_config),
            receive_timeout: 20_000,
            retry_count: 0
          }
          |> maybe_put_max_tokens(llm_config)

        llm = ChatOpenAI.new!(llm_opts)

        messages =
          [Message.new_system!(system_prompt(definition))]
          |> Kernel.++(Enum.map(trim_history(history), &history_message/1))
          |> Kernel.++([request_message(request)])

        chain =
          %{llm: llm}
          |> LLMChain.new!()
          |> LLMChain.add_messages(messages)

        case LLMChain.run(chain) do
          {:ok, updated_chain} ->
            content =
              updated_chain.last_message.content
              |> ContentPart.content_to_string()
              |> to_string()
              |> String.trim()

            {:ok, content}

          {:error, _failed_chain, error} ->
            {:error, format_generation_error(error)}

          {:error, error} ->
            {:error, format_generation_error(error)}

          other ->
            {:error, {:unexpected_llm_result, other}}
        end
      rescue
        error -> {:error, Exception.message(error)}
      catch
        kind, reason -> {:error, {kind, reason}}
      end
    end
  end

  defp apply_successful_reply(state, content) do
    request = state.pending_request || %{type: :opening}

    updated_history =
      case request do
        %{type: :user, content: user_content} ->
          state.history ++
            [%{role: :user, content: user_content}, %{role: :assistant, content: content}]

        _ ->
          state.history ++ [%{role: :assistant, content: content}]
      end

    state
    |> Map.put(:pending_request, nil)
    |> Map.put(:history, trim_history(updated_history))
    |> deliver_bot_message(content)
  end

  defp start_next_or_disconnect(state) do
    cond do
      state.bot_messages_sent >= state.max_messages ->
        Process.send_after(self(), :disconnect_bot, 1_500)
        state

      state.queued_user_messages == [] ->
        state

      true ->
        [next_message | rest] = state.queued_user_messages

        state
        |> Map.put(:queued_user_messages, rest)
        |> maybe_start_request(%{type: :user, content: next_message})
    end
  end

  defp deliver_bot_message(state, content) when is_binary(content) do
    trimmed = String.trim(content)

    if trimmed == "" do
      state
    else
      :telemetry.execute(
        [:omegle_phoenix, :bots, :reply_sent],
        %{count: 1},
        %{bot_type: "ai"}
      )

      OmeglePhoenix.Router.send_message(
        state.human_session_id,
        %{
          type: "message",
          from: state.bot_session_id,
          match_generation: state.match_generation,
          data: %{content: trimmed}
        }
      )

      next_state =
        state
        |> Map.put(:bot_messages_sent, state.bot_messages_sent + 1)
        |> reset_idle_timer()

      if next_state.bot_messages_sent >= next_state.max_messages do
        Process.send_after(self(), :disconnect_bot, 1_500)
      end

      next_state
    end
  end

  defp send_typing(state, typing) do
    if is_binary(state.match_generation) do
      OmeglePhoenix.Router.send_message(
        state.human_session_id,
        %{
          type: "typing",
          from: state.bot_session_id,
          match_generation: state.match_generation,
          data: %{typing: typing}
        }
      )
    end

    state
  end

  defp request_source(%{type: :opening}), do: "opening"
  defp request_source(%{type: :user}), do: "user"
  defp request_source(_request), do: "unknown"

  defp pair_with_human(human_session_id, bot_session_id) do
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
            bot_type: "ai",
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

  defp owner_node(session_id, route) do
    case OmeglePhoenix.Router.owner_node(session_id, route_hint: route) do
      {:ok, node_name} -> node_name
      _ -> nil
    end
  end

  defp disconnect_human_partner(state, reason) do
    with {:ok, bot_session} <- OmeglePhoenix.SessionManager.get_session(state.bot_session_id),
         {:ok, human_session} <- OmeglePhoenix.SessionManager.get_session(state.human_session_id),
         {:ok, _updated_bot, updated_human} <-
           OmeglePhoenix.SessionManager.reset_pair(bot_session, human_session) do
      match_generation = human_session.match_generation || state.match_generation

      OmeglePhoenix.Router.notify_disconnect(
        updated_human.id,
        reason,
        match_generation
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

  defp history_message(%{role: :user, content: content}), do: Message.new_user!(content)
  defp history_message(%{role: :assistant, content: content}), do: Message.new_assistant!(content)
  defp history_message(%{"role" => "user", "content" => content}), do: Message.new_user!(content)

  defp history_message(%{"role" => "assistant", "content" => content}),
    do: Message.new_assistant!(content)

  defp trim_history(history) when is_list(history) do
    history
    |> Enum.take(-@max_retained_history_messages)
  end

  defp trim_history(_history), do: []

  defp request_message(%{type: :opening}), do: Message.new_user!(@opening_instruction)
  defp request_message(%{type: :user, content: content}), do: Message.new_user!(content)

  defp ai_config(definition) do
    Map.get(definition, "ai_config_json", %{})
  end

  defp system_prompt(definition) do
    definition
    |> ai_config()
    |> Map.get("system_prompt", @default_system_prompt)
    |> case do
      prompt when is_binary(prompt) and prompt != "" -> prompt
      _ -> @default_system_prompt
    end
  end

  defp fallback_message(definition) do
    definition
    |> ai_config()
    |> Map.get("fallback_message", @default_fallback_message)
    |> case do
      message when is_binary(message) and message != "" -> message
      _ -> @default_fallback_message
    end
  end

  defp llm_temperature(ai_config) do
    case Map.get(ai_config, "temperature", 0.7) do
      value when is_number(value) and value >= 0 and value <= 2 -> value
      _ -> 0.7
    end
  end

  defp maybe_put_max_tokens(llm_opts, ai_config) do
    case Map.get(ai_config, "max_tokens", 0) do
      value when is_integer(value) and value > 0 -> Map.put(llm_opts, :max_tokens, value)
      _ -> llm_opts
    end
  end

  defp normalize_endpoint(nil), do: nil

  defp normalize_endpoint(endpoint) when is_binary(endpoint) do
    trimmed = String.trim(endpoint)

    cond do
      trimmed == "" ->
        nil

      String.contains?(trimmed, "/chat/completions") ->
        trimmed

      String.ends_with?(trimmed, "/") ->
        trimmed <> "chat/completions"

      true ->
        trimmed <> "/chat/completions"
    end
  end

  defp normalize_endpoint(endpoint), do: endpoint

  defp format_generation_error(%{message: message}) when is_binary(message) and message != "",
    do: message

  defp format_generation_error(%{type: type, message: message})
       when is_binary(type) and is_binary(message),
       do: "#{type}: #{message}"

  defp format_generation_error(error), do: inspect(error)

  defp max_messages(definition) do
    definition
    |> Map.get("message_limit", 20)
    |> case do
      value when is_integer(value) and value > 0 -> value
      _ -> 20
    end
  end

  defp idle_timeout_ms(definition) do
    definition
    |> Map.get("idle_timeout_seconds", 45)
    |> case do
      value when is_integer(value) and value > 0 -> value * 1_000
      _ -> 45_000
    end
  end

  defp session_ttl_ms(definition) do
    definition
    |> Map.get("session_ttl_seconds", 300)
    |> case do
      value when is_integer(value) and value > 0 -> value * 1_000
      _ -> 300_000
    end
  end

  defp reset_idle_timer(state) do
    cancel_timer(state.idle_timer)

    %{
      state
      | idle_timer: schedule_timer(:idle_timeout_reached, idle_timeout_ms(state.definition))
    }
  end

  defp schedule_timer(_message, delay_ms) when not is_integer(delay_ms) or delay_ms <= 0, do: nil
  defp schedule_timer(message, delay_ms), do: Process.send_after(self(), message, delay_ms)

  defp cancel_timer(nil), do: :ok
  defp cancel_timer(timer_ref), do: Process.cancel_timer(timer_ref)
end
