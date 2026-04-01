defmodule OmeglePhoenixWeb.RoomChannel do
  use Phoenix.Channel

  @max_messages_per_window 30
  @rate_window_ms 5_000

  @impl true
  def join("room:" <> mode, _payload, socket) when mode in ["lobby", "text", "video"] do
    {:ok,
     assign(socket, mode: mode, msg_count: 0, window_start: System.system_time(:millisecond))}
  end

  @impl true
  def handle_in("search", _payload, socket) do
    socket = teardown_existing_session(socket)
    client_ip = socket.assigns[:client_ip] || "unknown"
    preferences = build_preferences(socket, %{})

    case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
      nil ->
        session_id = UUID.uuid4()

        case OmeglePhoenix.SessionManager.create_session(session_id, client_ip, preferences) do
          {:ok, session} ->
            OmeglePhoenix.Router.register(session_id, self())
            OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)

            {:reply,
             {:ok, %{type: "searching", session_id: session_id, session_token: session.token}},
             assign(socket, :session_id, session_id)}

          {:error, _reason} ->
            {:reply, {:error, %{reason: "Failed to create session"}}, socket}
        end

      reason ->
        {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}
    end
  end

  def handle_in("start", %{"data" => data}, socket) do
    socket = teardown_existing_session(socket)
    client_ip = socket.assigns[:client_ip] || "unknown"
    preferences = build_preferences(socket, Map.get(data, "preferences", %{}))

    case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
      nil ->
        session_id = UUID.uuid4()

        case OmeglePhoenix.SessionManager.create_session(session_id, client_ip, preferences) do
          {:ok, session} ->
            OmeglePhoenix.Router.register(session_id, self())
            OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)

            {:reply,
             {:ok, %{type: "connected", session_id: session_id, session_token: session.token}},
             assign(socket, :session_id, session_id)}

          {:error, _reason} ->
            {:reply, {:error, %{reason: "Failed to create session"}}, socket}
        end

      reason ->
        {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}
    end
  end

  def handle_in("skip", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case with_session_partner_lock(session_id, fn session ->
           if session && session.partner_id do
             reset_match(session_id, session.partner_id, "partner skipped")
             OmeglePhoenix.Matchmaker.join_queue(session_id, session.preferences)
             :telemetry.execute([:omegle_phoenix, :room, :skipped], %{count: 1}, %{session_id: session_id})
             {:ok, %{type: "skipped"}}
           else
             {:error, %{reason: "No partner to skip"}}
           end
         end) do
      {:ok, payload} ->
        {:reply, {:ok, payload}, socket}

      {:error, payload} ->
        {:reply, {:error, payload}, socket}
    end
  end

  def handle_in("message", %{"data" => data}, socket) do
    session_id = socket.assigns[:session_id]

    if is_nil(session_id) do
      {:reply, {:error, %{reason: "No active session"}}, socket}
    else
      {socket, allowed} = check_rate_limit(socket)

      if not allowed do
        {:reply, {:error, %{reason: "Rate limit exceeded"}}, socket}
      else
        content = Map.get(data, "content")

        case OmeglePhoenix.SessionManager.get_session(session_id) do
          {:ok, session}
          when session.partner_id != nil and is_binary(content) and byte_size(content) <= 2_000 ->
            OmeglePhoenix.Router.send_message(session.partner_id, %{
              type: "message",
              from: session_id,
              data: %{content: content}
            })
            :telemetry.execute([:omegle_phoenix, :room, :message_sent], %{count: 1}, %{session_id: session_id})

            {:noreply, socket}

          {:ok, _session} ->
            {:reply, {:error, %{reason: "Invalid message content"}}, socket}

          _ ->
            {:reply, {:error, %{reason: "No partner"}}, socket}
        end
      end
    end
  end

  def handle_in("typing", %{"data" => data}, socket) do
    session_id = socket.assigns[:session_id]

    if is_nil(session_id) do
      {:noreply, socket}
    else
      is_typing = Map.get(data, "typing", false)

      case OmeglePhoenix.SessionManager.get_session(session_id) do
        {:ok, session} when session.partner_id != nil ->
          OmeglePhoenix.Router.send_message(session.partner_id, %{
            type: "typing",
            from: session_id,
            data: %{typing: is_typing}
          })
          :telemetry.execute([:omegle_phoenix, :room, :typing_sent], %{count: 1}, %{session_id: session_id, typing: is_typing})

          {:noreply, socket}

        _ ->
          {:noreply, socket}
      end
    end
  end

  def handle_in("webrtc_ready", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case with_session_partner_lock(session_id, fn session ->
           cond do
             is_nil(session) or is_nil(session.partner_id) ->
               {:error, %{reason: "No partner"}}

             true ->
               {:ok, updated_session} =
                 OmeglePhoenix.SessionManager.update_session(session_id, %{signaling_ready: true})

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

                   OmeglePhoenix.SessionManager.update_session(session_id, %{webrtc_started: true})
                   OmeglePhoenix.SessionManager.update_session(updated_session.partner_id, %{webrtc_started: true})
                   :telemetry.execute([:omegle_phoenix, :room, :webrtc_started], %{count: 1}, %{session_id: session_id, partner_id: updated_session.partner_id})
                   {:ok, %{type: "webrtc_ready"}}

                 _ ->
                   :telemetry.execute([:omegle_phoenix, :room, :webrtc_ready], %{count: 1}, %{session_id: session_id})
                   {:ok, %{type: "webrtc_ready"}}
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

    with_session_partner_lock(session_id, fn session ->
      if session do
        case session.partner_id do
          nil ->
            OmeglePhoenix.Matchmaker.leave_queue(session_id)

            OmeglePhoenix.SessionManager.update_session(session_id, %{
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

          partner_id ->
            reset_match(session_id, partner_id, "partner cancelled")
        end

        :telemetry.execute([:omegle_phoenix, :room, :stopped], %{count: 1}, %{session_id: session_id})
      end

      {:ok, %{type: "stopped"}}
    end)

    {:reply, {:ok, %{type: "stopped"}}, socket}
  end

  def handle_in("disconnect", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case close_session(session_id, "partner disconnected") do
      :ok ->
        {:reply, {:ok, %{type: "disconnected"}}, socket}

      {:error, :not_found} ->
        {:reply, {:error, %{reason: "Session not found"}}, socket}
    end
  end

  def handle_in("ping", _payload, socket) do
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
        _ -> Map.get(safe_prefs, "mode", "lobby")
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

        val =
          if is_binary(v),
            do: String.slice(v, 0, limit),
            else: to_string(v) |> String.slice(0, limit)

        Map.put(acc, key, val)
      else
        acc
      end
    end)
  end

  defp validate_preferences(_), do: %{}

  defp teardown_existing_session(socket) do
    if session_id = socket.assigns[:session_id] do
      _ = close_session(session_id, "partner disconnected")
    end

    socket
  end

  @impl true
  def handle_info({:router_match, partner_session_id, common_interests}, socket) do
    push(socket, "match", %{peer_id: partner_session_id, common_interests: common_interests})
    {:noreply, socket}
  end

  def handle_info({:router_message, %{type: type} = payload}, socket) do
    push(socket, type, Map.delete(payload, :type))
    {:noreply, socket}
  end

  def handle_info({:router_disconnect, reason}, socket) do
    push(socket, "disconnected", %{reason: reason})
    {:noreply, socket}
  end

  def handle_info(:router_timeout, socket) do
    push(socket, "timeout", %{})
    {:noreply, socket}
  end

  def handle_info({:router_banned, reason}, socket) do
    push(socket, "banned", %{reason: reason})
    {:noreply, socket}
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

        OmeglePhoenix.Matchmaker.leave_queue(session_id)
        OmeglePhoenix.Router.unregister(session_id)
        OmeglePhoenix.SessionManager.delete_session(session_id)
        :telemetry.execute([:omegle_phoenix, :room, :disconnected], %{count: 1}, %{session_id: session_id})
        :ok
      else
        {:error, :not_found}
      end
    end)
  end

  defp reset_match(session_id, partner_id, disconnect_reason) do
    OmeglePhoenix.Router.notify_disconnect(partner_id, disconnect_reason)

    with {:ok, session} <- OmeglePhoenix.SessionManager.get_session(session_id),
         {:ok, partner_session} <- OmeglePhoenix.SessionManager.get_session(partner_id),
         {:ok, _updated_session, _updated_partner} <-
           OmeglePhoenix.SessionManager.reset_pair(session, partner_session) do
      :ok
    else
      _ -> :ok
    end
  end

  defp with_session_partner_lock(nil, fun), do: fun.(nil)

  defp with_session_partner_lock(session_id, fun) do
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

           fun.(session)
         end) do
      {:error, :locked} ->
        {:error, %{reason: "Session busy, please retry"}}

      result ->
        result
    end
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
end
