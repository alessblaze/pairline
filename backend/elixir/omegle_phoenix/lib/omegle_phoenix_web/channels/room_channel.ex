defmodule OmeglePhoenixWeb.RoomChannel do
  use Phoenix.Channel
  require Logger

  @max_messages_per_window 30
  @rate_window_ms 5_000
  @session_refresh_ms 60_000
  @owner_refresh_ms 10_000

  @impl true
  def join("room:" <> mode, _payload, socket) when mode in ["lobby", "text", "video"] do
    schedule_session_refresh()
    schedule_owner_refresh()

    {:ok,
     assign(socket,
       mode: mode,
       last_typing_at: nil,
       partner_id: nil,
       msg_count: 0,
       window_start: System.system_time(:millisecond)
     )}
  end

  def join("room:" <> _mode, _payload, _socket) do
    {:error, %{reason: "Unsupported room"}}
  end

  @impl true
  def handle_in("search", _payload, socket) do
    case teardown_existing_session(socket) do
      {:error, reason, socket} ->
        {:reply, {:error, %{reason: reason}}, socket}

      {:ok, socket} ->
        client_ip = socket.assigns[:client_ip] || "unknown"
        preferences = build_preferences(socket, %{})

        case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
          nil ->
            create_and_queue_session(socket, client_ip, preferences, "searching")

          reason ->
            {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}
        end
    end
  end

  def handle_in("start", %{"data" => data}, socket) when is_map(data) do
    case teardown_existing_session(socket) do
      {:error, reason, socket} ->
        {:reply, {:error, %{reason: reason}}, socket}

      {:ok, socket} ->
        client_ip = socket.assigns[:client_ip] || "unknown"
        preferences = build_preferences(socket, Map.get(data, "preferences", %{}))

        case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
          nil ->
            create_and_queue_session(socket, client_ip, preferences, "connected")

          reason ->
            {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}
        end
    end
  end

  def handle_in("start", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid start payload"}}, socket}
  end

  def handle_in("skip", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case with_session_partner_lock(session_id, fn session ->
           if session && session.partner_id do
             reset_match(session_id, session.partner_id, "partner skipped")

             case OmeglePhoenix.Matchmaker.join_queue(session_id, session.preferences) do
               :ok ->
                 :telemetry.execute([:omegle_phoenix, :room, :skipped], %{count: 1}, %{
                   session_id: session_id
                 })

                 {:ok, %{type: "skipped"}}

               {:error, _reason} ->
                 _ =
                   OmeglePhoenix.SessionManager.update_session(session_id, %{
                     status: :disconnecting,
                     signaling_ready: false,
                     webrtc_started: false
                   })

                 {:error, %{reason: "Matchmaking unavailable"}}
             end
           else
             {:error, %{reason: "No partner to skip"}}
           end
         end) do
      {:ok, payload} ->
        {:reply, {:ok, payload}, assign(socket, :partner_id, nil)}

      {:error, payload} ->
        {:reply, {:error, payload}, socket}
    end
  end

  def handle_in("message", %{"data" => data}, socket) when is_map(data) do
    session_id = socket.assigns[:session_id]
    partner_id = socket.assigns[:partner_id]

    if is_nil(session_id) do
      {:reply, {:error, %{reason: "No active session"}}, socket}
    else
      if is_nil(partner_id) do
        # Return ok to swallow error for in-flight messages sent right at disconnect time
        Logger.debug("Swallowed in-flight message from #{session_id}: no partner assigned")
        {:reply, {:ok, %{status: "ignored"}}, socket}
      else
        {socket, allowed} = check_rate_limit(socket)

        if not allowed do
          {:reply, {:error, %{reason: "Rate limit exceeded"}}, socket}
        else
          content = Map.get(data, "content")

          if is_binary(content) and byte_size(content) <= 2_000 do
            case with_current_partner(session_id, partner_id, fn ->
                   OmeglePhoenix.Router.send_message(partner_id, %{
                     type: "message",
                     from: session_id,
                     data: %{content: content}
                   })

                   :telemetry.execute([:omegle_phoenix, :room, :message_sent], %{count: 1}, %{
                     session_id: session_id
                   })

                   {:ok, :sent}
                 end) do
              {:ok, :sent} ->
                {:noreply, socket}

              {:error, %{reason: "Partner changed"}} ->
                # In-flight race, visually do nothing instead of rendering an error.
                Logger.debug("Swallowed in-flight message from #{session_id}: partner changed")
                {:reply, {:ok, %{status: "ignored"}}, assign(socket, :partner_id, nil)}

              {:error, %{type: "ignored", reason: _}} ->
                Logger.debug("Swallowed in-flight message from #{session_id}: post-disconnect")
                {:reply, {:ok, %{status: "ignored"}}, assign(socket, :partner_id, nil)}

              {:error, payload} ->
                {:reply, {:error, payload}, socket}
            end
          else
            {:reply, {:error, %{reason: "Invalid message content"}}, socket}
          end
        end
      end
    end
  end

  def handle_in("message", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid message payload"}}, socket}
  end

  def handle_in("typing", %{"data" => data}, socket) when is_map(data) do
    session_id = socket.assigns[:session_id]
    partner_id = socket.assigns[:partner_id]

    if is_nil(session_id) or is_nil(partner_id) do
      {:noreply, socket}
    else
      case Map.fetch(data, "typing") do
        {:ok, is_typing} when is_boolean(is_typing) ->
          {socket, allowed} = check_typing_rate_limit(socket)

          if allowed do
            case with_current_partner(session_id, partner_id, fn ->
                   OmeglePhoenix.Router.send_message(partner_id, %{
                     type: "typing",
                     from: session_id,
                     data: %{typing: is_typing}
                   })

                   :telemetry.execute([:omegle_phoenix, :room, :typing_sent], %{count: 1}, %{
                     session_id: session_id,
                     typing: is_typing
                   })

                   {:ok, :sent}
                 end) do
              {:ok, :sent} ->
                :ok

              {:error, _payload} ->
                :ok
            end
          end

          {:noreply, socket}

        _ ->
          {:reply, {:error, %{reason: "Invalid typing payload"}}, socket}
      end
    end
  end

  def handle_in("typing", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid typing payload"}}, socket}
  end

  def handle_in("webrtc_ready", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case with_session_partner_lock(session_id, fn session ->
           cond do
             is_nil(session) or is_nil(session.partner_id) ->
               {:error, %{reason: "No partner"}}

             true ->
               with {:ok, updated_session} <-
                      OmeglePhoenix.SessionManager.update_session(session_id, %{
                        signaling_ready: true
                      }) do
                 case OmeglePhoenix.SessionManager.get_session(updated_session.partner_id) do
                   {:ok, partner_session}
                   when partner_session.signaling_ready == true and
                          updated_session.webrtc_started != true and
                          partner_session.webrtc_started != true ->
                     OmeglePhoenix.Router.send_message(session_id, %{
                       type: "webrtc_start",
                       peer_id: updated_session.partner_id
                     })

                     OmeglePhoenix.Router.send_message(updated_session.partner_id, %{
                       type: "webrtc_start",
                       peer_id: session_id
                     })

                     _ =
                       OmeglePhoenix.SessionManager.update_session(session_id, %{
                         webrtc_started: true
                       })

                     _ =
                       OmeglePhoenix.SessionManager.update_session(updated_session.partner_id, %{
                         webrtc_started: true
                       })

                     :telemetry.execute(
                       [:omegle_phoenix, :room, :webrtc_started],
                       %{count: 1},
                       %{session_id: session_id, partner_id: updated_session.partner_id}
                     )

                     {:ok, %{type: "webrtc_ready"}}

                   _ ->
                     :telemetry.execute(
                       [:omegle_phoenix, :room, :webrtc_ready],
                       %{count: 1},
                       %{session_id: session_id}
                     )

                     {:ok, %{type: "webrtc_ready"}}
                 end
               else
                 {:error, :not_found} ->
                   {:error, %{reason: "Session not found"}}

                 {:error, _reason} ->
                   {:error, %{reason: "Failed to update session"}}
               end
           end
         end) do
      {:ok, payload} ->
        {:reply, {:ok, payload}, socket}

      {:error, payload} ->
        {:reply, {:error, payload}, socket}
    end
  end

  def handle_in("stop", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case with_session_partner_lock(session_id, fn session ->
           cond do
             is_nil(session) ->
               {:ok, %{type: "stopped"}}

             is_nil(session.partner_id) ->
               with :ok <- OmeglePhoenix.Matchmaker.leave_queue(session_id),
                    {:ok, _updated_session} <-
                      OmeglePhoenix.SessionManager.update_session(session_id, %{
                        status: :waiting,
                        signaling_ready: false,
                        webrtc_started: false
                      }) do
                 :telemetry.execute([:omegle_phoenix, :room, :stopped], %{count: 1}, %{
                   session_id: session_id
                 })

                 {:ok, %{type: "stopped"}}
               else
                 {:error, _reason} ->
                   {:error, %{reason: "Failed to stop matchmaking"}}
               end

             true ->
               reset_match(session_id, session.partner_id, "partner cancelled")

               :telemetry.execute([:omegle_phoenix, :room, :stopped], %{count: 1}, %{
                 session_id: session_id
               })

               {:ok, %{type: "stopped"}}
           end
         end) do
      {:ok, payload} ->
        {:reply, {:ok, payload}, assign(socket, :partner_id, nil)}

      {:error, payload} ->
        {:reply, {:error, payload}, socket}
    end
  end

  def handle_in("disconnect", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case close_session(session_id, "partner disconnected") do
      :ok ->
        {:reply, {:ok, %{type: "disconnected"}}, clear_session_assigns(socket)}

      {:error, :not_found} ->
        {:reply, {:error, %{reason: "Session not found"}}, socket}

      {:error, %{reason: reason}} ->
        {:reply, {:error, %{reason: reason}}, socket}
    end
  end

  def handle_in("ping", _payload, socket) do
    if session_id = socket.assigns[:session_id] do
      _ = OmeglePhoenix.SessionManager.refresh_session(session_id)
      _ = OmeglePhoenix.Router.refresh_owner(session_id, self())
    end

    {:reply, {:ok, %{type: "pong"}}, socket}
  end

  def handle_in(_event, _payload, socket) do
    {:noreply, socket}
  end

  defp build_preferences(socket, preferences) do
    safe_prefs = validate_preferences(preferences)

    mode =
      case socket.assigns[:mode] do
        "text" -> "text"
        "video" -> "video"
        _ -> normalize_mode(Map.get(safe_prefs, "mode"), "lobby")
      end

    Map.put(safe_prefs, "mode", mode)
  end

  # Strict allowlist: only known keys are preserved, all others are silently dropped.
  # This prevents DoS via memory exhaustion from arbitrary key/value pairs.
  @allowed_preference_keys ["mode", "interests", "max_wait", "tags"]

  defp validate_preferences(prefs) when is_map(prefs) do
    Enum.reduce(prefs, %{}, fn {k, v}, acc ->
      key = if is_binary(k), do: k, else: to_string(k)

      if key in @allowed_preference_keys do
        limit = if key == "interests", do: 255, else: 50

        case normalize_preference_value(v, limit) do
          nil ->
            acc

          val ->
            Map.put(acc, key, val)
        end
      else
        acc
      end
    end)
  end

  defp validate_preferences(_), do: %{}

  defp normalize_preference_value(value, limit) do
    case preference_to_string(value) do
      nil -> nil
      string -> String.slice(string, 0, limit)
    end
  end

  defp preference_to_string(value) when is_binary(value), do: value
  defp preference_to_string(value) when is_boolean(value), do: to_string(value)
  defp preference_to_string(value) when is_integer(value), do: Integer.to_string(value)

  defp preference_to_string(value) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp preference_to_string(value) when is_atom(value), do: Atom.to_string(value)

  defp preference_to_string(value) when is_list(value) do
    if Enum.all?(value, &preference_scalar?/1) do
      value
      |> Enum.map(&preference_to_string/1)
      |> Enum.join(",")
    else
      nil
    end
  end

  defp preference_to_string(_value), do: nil

  defp preference_scalar?(value) do
    is_binary(value) or is_boolean(value) or is_integer(value) or is_float(value) or
      is_atom(value)
  end

  defp normalize_mode(mode, _default) when mode in ["lobby", "text", "video"], do: mode
  defp normalize_mode(_mode, default), do: default

  defp teardown_existing_session(socket) do
    if session_id = socket.assigns[:session_id] do
      case close_session(session_id, "partner disconnected") do
        :ok ->
          {:ok, clear_session_assigns(socket)}

        {:error, :not_found} ->
          {:ok, clear_session_assigns(socket)}

        {:error, %{reason: reason}} ->
          {:error, reason, socket}
      end
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_info(:refresh_session, socket) do
    if session_id = socket.assigns[:session_id] do
      _ = OmeglePhoenix.SessionManager.refresh_session(session_id)
    end

    schedule_session_refresh()
    {:noreply, socket}
  end

  def handle_info(:refresh_owner, socket) do
    if session_id = socket.assigns[:session_id] do
      _ = OmeglePhoenix.Router.refresh_owner(session_id, self())
    end

    schedule_owner_refresh()
    {:noreply, socket}
  end

  def handle_info({:router_match, partner_session_id, common_interests}, socket) do
    push(socket, "match", %{peer_id: partner_session_id, common_interests: common_interests})
    {:noreply, assign(socket, :partner_id, partner_session_id)}
  end

  def handle_info({:router_message, %{type: type} = payload}, socket) do
    push(socket, type, Map.delete(payload, :type))
    {:noreply, socket}
  end

  def handle_info({:router_message, %{"type" => type} = payload}, socket) do
    push(socket, type, Map.delete(payload, "type"))
    {:noreply, socket}
  end

  def handle_info({:router_disconnect, reason}, socket) do
    push(socket, "disconnected", %{reason: reason})
    {:noreply, assign(socket, :partner_id, nil)}
  end

  def handle_info(:router_timeout, socket) do
    push(socket, "timeout", %{})
    {:noreply, assign(socket, :partner_id, nil)}
  end

  def handle_info({:router_banned, reason}, socket) do
    push(socket, "banned", %{reason: reason})
    {:noreply, assign(socket, :partner_id, nil)}
  end

  def handle_info(_message, socket) do
    {:noreply, socket}
  end

  @impl true
  def terminate(_reason, socket) do
    if session_id = socket.assigns[:session_id] do
      _ = close_session(session_id, "partner disconnected")
    end

    :ok
  end

  defp close_session(nil, _reason), do: {:error, :not_found}

  defp close_session(session_id, reason) do
    with_session_partner_lock(session_id, fn session ->
      if session do
        if session.partner_id do
          reset_match(session_id, session.partner_id, reason)
        end

        case OmeglePhoenix.Matchmaker.leave_queue(session_id) do
          :ok ->
            :ok

          {:error, _reason} ->
            return_error = {:error, %{reason: "Failed to leave matchmaking queue"}}
            throw({:close_session_error, return_error})
        end

        OmeglePhoenix.Router.unregister(session_id)

        case OmeglePhoenix.SessionManager.delete_session(session_id) do
          :ok ->
            :ok

          {:error, _reason} ->
            return_error = {:error, %{reason: "Failed to delete session"}}
            throw({:close_session_error, return_error})
        end

        :telemetry.execute([:omegle_phoenix, :room, :disconnected], %{count: 1}, %{
          session_id: session_id
        })

        :ok
      else
        {:error, :not_found}
      end
    end)
  catch
    {:close_session_error, error} ->
      error
  end

  defp reset_match(session_id, partner_id, disconnect_reason) do
    with {:ok, session} <- OmeglePhoenix.SessionManager.get_session(session_id),
         {:ok, partner_session} <- OmeglePhoenix.SessionManager.get_session(partner_id),
         {:ok, _updated_session, _updated_partner} <-
           OmeglePhoenix.SessionManager.reset_pair(session, partner_session) do
      OmeglePhoenix.Router.notify_disconnect(partner_id, disconnect_reason)
      :ok
    else
      _ -> :ok
    end
  end

  defp with_session_partner_lock(nil, fun), do: fun.(nil)

  defp with_session_partner_lock(session_id, fun),
    do: with_session_partner_lock(session_id, fun, 3)

  defp with_session_partner_lock(_session_id, _fun, 0) do
    {:error, %{reason: "Session busy, please retry"}}
  end

  defp with_session_partner_lock(session_id, fun, attempts_left) do
    partner_id =
      case OmeglePhoenix.SessionManager.get_session(session_id) do
        {:ok, session} -> session.partner_id
        _ -> nil
      end

    case OmeglePhoenix.SessionLock.with_locks([session_id, partner_id], fn ->
           session =
             case OmeglePhoenix.SessionManager.get_session(session_id) do
               {:ok, current_session} -> current_session
               _ -> nil
             end

           cond do
             is_nil(session) ->
               fun.(nil)

             session.partner_id != partner_id ->
               {:retry, session.partner_id}

             true ->
               fun.(session)
           end
         end) do
      {:error, :locked} ->
        {:error, %{reason: "Session busy, please retry"}}

      {:retry, _updated_partner_id} ->
        with_session_partner_lock(session_id, fun, attempts_left - 1)

      result ->
        result
    end
  end

  defp with_current_partner(session_id, expected_partner_id, fun) do
    with_session_partner_lock(session_id, fn session ->
      cond do
        is_nil(session) ->
          {:error, %{reason: "Session not found"}}

        is_nil(session.partner_id) or session.status != :matched ->
          {:error, %{type: "ignored", reason: "Partner changed"}}

        session.partner_id != expected_partner_id ->
          {:error, %{reason: "Partner changed"}}

        true ->
          case OmeglePhoenix.SessionManager.get_session(expected_partner_id) do
            {:ok, partner_session}
            when partner_session.partner_id == session_id and partner_session.status == :matched ->
              fun.()

            _ ->
              {:error, %{reason: "Partner changed"}}
          end
      end
    end)
  end

  defp create_and_queue_session(socket, client_ip, preferences, connected_type) do
    session_id = UUID.uuid4()

    case OmeglePhoenix.SessionManager.create_session(session_id, client_ip, preferences) do
      {:ok, session} ->
        OmeglePhoenix.Router.register(session_id, self())

        case OmeglePhoenix.Matchmaker.join_queue(session_id, preferences) do
          :ok ->
            {:reply,
             {:ok, %{type: connected_type, session_id: session_id, session_token: session.token}},
             socket
             |> assign(:session_id, session_id)
             |> assign(:partner_id, nil)}

          {:error, _reason} ->
            OmeglePhoenix.Router.unregister(session_id)
            _ = OmeglePhoenix.SessionManager.delete_session(session_id)
            {:reply, {:error, %{reason: "Matchmaking unavailable"}}, socket}
        end

      {:error, _reason} ->
        {:reply, {:error, %{reason: "Failed to create session"}}, socket}
    end
  end

  defp clear_session_assigns(socket) do
    socket
    |> assign(:session_id, nil)
    |> assign(:partner_id, nil)
  end

  defp check_rate_limit(socket) do
    now = System.system_time(:millisecond)
    window_start = socket.assigns[:window_start] || now
    msg_count = socket.assigns[:msg_count] || 0

    if now - window_start > @rate_window_ms do
      # Reset window
      {assign(socket, msg_count: 1, window_start: now), true}
    else
      new_count = msg_count + 1

      if new_count > @max_messages_per_window do
        {socket, false}
      else
        {assign(socket, :msg_count, new_count), true}
      end
    end
  end

  defp check_typing_rate_limit(socket) do
    now = System.system_time(:millisecond)
    last_typing_at = socket.assigns[:last_typing_at]

    if is_integer(last_typing_at) and now - last_typing_at < 250 do
      {socket, false}
    else
      {assign(socket, :last_typing_at, now), true}
    end
  end

  defp schedule_session_refresh do
    Process.send_after(self(), :refresh_session, @session_refresh_ms)
  end

  defp schedule_owner_refresh do
    Process.send_after(self(), :refresh_owner, @owner_refresh_ms)
  end
end
