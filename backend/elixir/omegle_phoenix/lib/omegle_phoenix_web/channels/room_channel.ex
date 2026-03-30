defmodule OmeglePhoenixWeb.RoomChannel do
  use Phoenix.Channel

  @impl true
  def join("room:" <> mode, _payload, socket) when mode in ["lobby", "text", "video"] do
    {:ok, assign(socket, :mode, mode)}
  end

  @impl true
  def handle_in("search", _payload, socket) do
    socket = teardown_existing_session(socket)
    client_ip = socket.assigns[:client_ip] || "unknown"
    preferences = build_preferences(socket, %{})

    case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
      nil ->
        session_id = UUID.uuid4()

        {:ok, session} =
          OmeglePhoenix.SessionManager.create_session(session_id, client_ip, preferences)

        OmeglePhoenix.Router.register(session_id, self())
        OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)

        {:reply,
         {:ok, %{type: "searching", session_id: session_id, session_token: session.token}},
         assign(socket, :session_id, session_id)}

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

        {:ok, session} =
          OmeglePhoenix.SessionManager.create_session(session_id, client_ip, preferences)

        OmeglePhoenix.Router.register(session_id, self())
        OmeglePhoenix.Matchmaker.join_queue(session_id, preferences)

        {:reply,
         {:ok, %{type: "connected", session_id: session_id, session_token: session.token}},
         assign(socket, :session_id, session_id)}

      reason ->
        {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}
    end
  end

  def handle_in("skip", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} when session.partner_id != nil ->
        OmeglePhoenix.Router.notify_disconnect(session.partner_id, "partner skipped")

        OmeglePhoenix.SessionManager.update_session(session.partner_id, %{
          partner_id: nil,
          status: :waiting,
          signaling_ready: false,
          webrtc_started: false
        })

        OmeglePhoenix.SessionManager.update_session(session_id, %{
          partner_id: nil,
          status: :waiting,
          signaling_ready: false,
          webrtc_started: false
        })

        OmeglePhoenix.Redis.command(["DEL", "match:#{session_id}"])
        OmeglePhoenix.Redis.command(["DEL", "match:#{session.partner_id}"])

        OmeglePhoenix.Matchmaker.join_queue(session_id, session.preferences)

        {:reply, {:ok, %{type: "skipped"}}, socket}

      _ ->
        {:reply, {:error, %{reason: "No partner to skip"}}, socket}
    end
  end

  def handle_in("message", %{"data" => data}, socket) do
    session_id = socket.assigns[:session_id]
    content = Map.get(data, "content")

    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session}
      when session.partner_id != nil and is_binary(content) and byte_size(content) <= 2_000 ->
        OmeglePhoenix.Router.send_message(session.partner_id, %{
          type: "message",
          from: session_id,
          data: %{content: content}
        })

        {:noreply, socket}

      {:ok, _session} ->
        {:reply, {:error, %{reason: "Invalid message content"}}, socket}

      _ ->
        {:reply, {:error, %{reason: "No partner"}}, socket}
    end
  end

  def handle_in("typing", %{"data" => data}, socket) do
    session_id = socket.assigns[:session_id]
    is_typing = Map.get(data, "typing", false)

    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} when session.partner_id != nil ->
        OmeglePhoenix.Router.send_message(session.partner_id, %{
          type: "typing",
          from: session_id,
          data: %{typing: is_typing}
        })

        {:noreply, socket}

      _ ->
        {:noreply, socket}
    end
  end

  def handle_in("webrtc_ready", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} when session.partner_id != nil ->
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

            OmeglePhoenix.SessionManager.update_session(updated_session.partner_id, %{
              webrtc_started: true
            })

            {:reply, {:ok, %{type: "webrtc_ready"}}, socket}

          _ ->
            {:reply, {:ok, %{type: "webrtc_ready"}}, socket}
        end

      _ ->
        {:reply, {:error, %{reason: "No partner"}}, socket}
    end
  end

  def handle_in("stop", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} ->
        case session.partner_id do
          nil ->
            OmeglePhoenix.Matchmaker.leave_queue(session_id)

            OmeglePhoenix.SessionManager.update_session(session_id, %{
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

          partner_id ->
            OmeglePhoenix.Router.notify_disconnect(partner_id, "partner cancelled")

            OmeglePhoenix.SessionManager.update_session(partner_id, %{
              partner_id: nil,
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

            OmeglePhoenix.SessionManager.update_session(session_id, %{
              partner_id: nil,
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

            OmeglePhoenix.Redis.command(["DEL", "match:#{session_id}"])
            OmeglePhoenix.Redis.command(["DEL", "match:#{partner_id}"])
        end

        {:reply, {:ok, %{type: "stopped"}}, socket}

      _ ->
        {:reply, {:ok, %{type: "stopped"}}, socket}
    end
  end

  def handle_in("disconnect", _payload, socket) do
    session_id = socket.assigns[:session_id]

    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} ->
        case session.partner_id do
          nil ->
            :ok

          partner_id ->
            OmeglePhoenix.Router.notify_disconnect(partner_id, "partner disconnected")

            OmeglePhoenix.SessionManager.update_session(partner_id, %{
              partner_id: nil,
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

            OmeglePhoenix.Redis.command(["DEL", "match:#{session_id}"])
            OmeglePhoenix.Redis.command(["DEL", "match:#{partner_id}"])
        end

        OmeglePhoenix.Matchmaker.leave_queue(session_id)
        OmeglePhoenix.Router.unregister(session_id)
        OmeglePhoenix.SessionManager.delete_session(session_id)

        {:reply, {:ok, %{type: "disconnected"}}, socket}

      _ ->
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
    safe_prefs = sanitize_preferences(preferences)
    mode =
      case socket.assigns[:mode] do
        "text" -> "text"
        "video" -> "video"
        _ -> Map.get(safe_prefs, "mode", "lobby")
      end

    Map.put(safe_prefs, "mode", mode)
  end

  defp sanitize_preferences(prefs) when is_map(prefs) do
    prefs
    |> Enum.take(5)
    |> Map.new(fn {k, v} ->
      key = if is_binary(k), do: String.slice(k, 0, 50), else: to_string(k) |> String.slice(0, 50)
      
      limit = if key == "interests", do: 255, else: 50
      val = if is_binary(v), do: String.slice(v, 0, limit), else: to_string(v) |> String.slice(0, limit)
      
      {key, val}
    end)
  end

  defp sanitize_preferences(_), do: %{}

  defp teardown_existing_session(socket) do
    if session_id = socket.assigns[:session_id] do
      case OmeglePhoenix.SessionManager.get_session(session_id) do
        {:ok, session} ->
          if session.partner_id do
            OmeglePhoenix.Router.notify_disconnect(session.partner_id, "partner disconnected")

            OmeglePhoenix.SessionManager.update_session(session.partner_id, %{
              partner_id: nil,
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

            OmeglePhoenix.Redis.command(["DEL", "match:#{session_id}"])
            OmeglePhoenix.Redis.command(["DEL", "match:#{session.partner_id}"])
          end

          OmeglePhoenix.Matchmaker.leave_queue(session_id)
          OmeglePhoenix.Router.unregister(session_id)
          OmeglePhoenix.SessionManager.delete_session(session_id)

        _ ->
          :ok
      end
    end

    socket
  end

  @impl true
  def handle_info({:match, partner_session_id, common_interests}, socket) do
    push(socket, "match", %{peer_id: partner_session_id, common_interests: common_interests})
    {:noreply, socket}
  end

  def handle_info({:message, %{type: type} = payload}, socket) do
    push(socket, type, Map.delete(payload, :type))
    {:noreply, socket}
  end

  def handle_info({:disconnect, reason}, socket) do
    push(socket, "disconnected", %{reason: reason})
    {:noreply, socket}
  end

  def handle_info(:timeout, socket) do
    push(socket, "timeout", %{})
    {:noreply, socket}
  end

  def handle_info(_message, socket) do
    {:noreply, socket}
  end

  @impl true
  def terminate(_reason, socket) do
    if session_id = socket.assigns[:session_id] do
      case OmeglePhoenix.SessionManager.get_session(session_id) do
        {:ok, session} ->
          if session.partner_id do
            OmeglePhoenix.Router.notify_disconnect(session.partner_id, "partner disconnected")

            OmeglePhoenix.SessionManager.update_session(session.partner_id, %{
              partner_id: nil,
              status: :waiting,
              signaling_ready: false,
              webrtc_started: false
            })

            OmeglePhoenix.Redis.command(["DEL", "match:#{session_id}"])
            OmeglePhoenix.Redis.command(["DEL", "match:#{session.partner_id}"])
          end

          OmeglePhoenix.Matchmaker.leave_queue(session_id)
          OmeglePhoenix.Router.unregister(session_id)
          OmeglePhoenix.SessionManager.delete_session(session_id)

        _ ->
          :ok
      end
    end

    :ok
  end
end
