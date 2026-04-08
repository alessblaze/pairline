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

defmodule OmeglePhoenixWeb.RoomChannel do
  use Phoenix.Channel
  require Logger
  require OpenTelemetry.Tracer, as: Tracer
  alias OmeglePhoenix.Tracing

  @max_messages_per_window 30
  @rate_window_ms 5_000
  @session_refresh_ms 60_000
  @owner_refresh_ms 20_000
  @webrtc_ready_retry_attempts 4
  @webrtc_ready_retry_delay_ms 75

  @impl true
  def join("room:" <> mode, _payload, socket) when mode in ["lobby", "text", "video"] do
    schedule_session_refresh()
    schedule_owner_refresh()

    {:ok,
     assign(socket,
       mode: mode,
       last_typing_at: nil,
       partner_id: nil,
       match_generation: nil,
       partner_route: nil,
       partner_owner_node: nil,
       msg_count: 0,
       window_start: System.system_time(:millisecond)
     )}
  end

  def join("room:" <> _mode, _payload, _socket) do
    {:error, %{reason: "Unsupported room"}}
  end

  @impl true
  def handle_in("search", %{"data" => data}, socket) when is_map(data) do
    Tracer.with_span "phoenix.room.search", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.search")
      Tracer.set_attributes(channel_span_attributes(socket))
      {socket, allowed} = check_rate_limit(socket)

      if not allowed do
        Tracer.set_attribute("room.rate_limited", true)
        {:reply, {:error, %{reason: "Rate limit exceeded"}}, socket}
      else
        case teardown_existing_session(socket) do
          {:error, reason, socket} ->
            Tracer.set_attribute("room.teardown_error", reason)
            {:reply, {:error, %{reason: reason}}, socket}

          {:ok, socket} ->
            with_captcha_verified(socket, data, fn socket ->
              preferences = build_preferences(socket, %{})
              client_ip = socket.assigns[:client_ip] || "unknown"

              Tracer.set_attributes(%{
                "client.ip_hash" => Tracing.safe_ref(client_ip),
                "room.interests_count" => interests_count(preferences)
              })

              case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
                {:ok, nil} ->
                  create_and_queue_session(socket, client_ip, preferences, "searching")

                {:ok, reason} ->
                  Tracer.set_attribute("room.banned", true)
                  {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}

                {:error, _reason} ->
                  {:reply, {:error, %{reason: "Unable to verify access right now"}}, socket}
              end
            end)
        end
      end
    end
  end

  def handle_in("search", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid search payload"}}, socket}
  end

  def handle_in("start", %{"data" => data}, socket) when is_map(data) do
    Tracer.with_span "phoenix.room.start", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.start")
      Tracer.set_attributes(channel_span_attributes(socket))
      {socket, allowed} = check_rate_limit(socket)

      if not allowed do
        Tracer.set_attribute("room.rate_limited", true)
        {:reply, {:error, %{reason: "Rate limit exceeded"}}, socket}
      else
        case teardown_existing_session(socket) do
          {:error, reason, socket} ->
            Tracer.set_attribute("room.teardown_error", reason)
            {:reply, {:error, %{reason: reason}}, socket}

          {:ok, socket} ->
            with_captcha_verified(socket, data, fn socket ->
              preferences = build_preferences(socket, Map.get(data, "preferences", %{}))
              client_ip = socket.assigns[:client_ip] || "unknown"

              Tracer.set_attributes(%{
                "client.ip_hash" => Tracing.safe_ref(client_ip),
                "room.preference_keys" => preferences |> Map.keys() |> length()
              })

              case OmeglePhoenix.SessionManager.ip_ban_reason(client_ip) do
                {:ok, nil} ->
                  create_and_queue_session(socket, client_ip, preferences, "connected")

                {:ok, reason} ->
                  Tracer.set_attribute("room.banned", true)
                  {:reply, {:error, %{type: "banned", data: %{reason: reason}}}, socket}

                {:error, _reason} ->
                  {:reply, {:error, %{reason: "Unable to verify access right now"}}, socket}
              end
            end)
        end
      end
    end
  end

  def handle_in("start", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid start payload"}}, socket}
  end

  def handle_in("skip", _payload, socket) do
    Tracer.with_span "phoenix.room.skip", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.skip")
      Tracer.set_attributes(channel_span_attributes(socket))
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
          {:reply, {:ok, payload}, clear_match_assigns(socket)}

        {:error, payload} ->
          {:reply, {:error, payload}, socket}
      end
    end
  end

  def handle_in("message", %{"data" => data}, socket) when is_map(data) do
    Tracer.with_span "phoenix.room.message", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.message")
      session_id = socket.assigns[:session_id]
      partner_id = socket.assigns[:partner_id]
      match_generation = socket.assigns[:match_generation]
      partner_route = socket.assigns[:partner_route]
      partner_owner_node = socket.assigns[:partner_owner_node]

      Tracer.set_attributes(
        Map.merge(channel_span_attributes(socket), %{
          "room.partner_ref" => Tracing.safe_ref(partner_id || ""),
          "room.has_match_generation" => is_binary(match_generation)
        })
      )

      if is_nil(session_id) do
        {:reply, {:error, %{reason: "No active session"}}, socket}
      else
        if is_nil(partner_id) do
          Logger.debug("Swallowed in-flight message from #{session_id}: no partner assigned")
          {:reply, {:ok, %{status: "ignored"}}, socket}
        else
          {socket, allowed} = check_rate_limit(socket)

          if not allowed do
            Tracer.set_attribute("room.rate_limited", true)
            {:reply, {:error, %{reason: "Rate limit exceeded"}}, socket}
          else
            content = Map.get(data, "content")

            Tracer.set_attribute(
              "room.message_length",
              if(is_binary(content), do: byte_size(content), else: 0)
            )

            if is_binary(content) and byte_size(content) <= 2_000 do
              if is_binary(match_generation) and is_map(partner_route) do
                OmeglePhoenix.Router.send_message(
                  partner_id,
                  %{
                    type: "message",
                    from: session_id,
                    match_generation: match_generation,
                    data: %{content: content}
                  },
                  route_hint: partner_route,
                  owner_hint: partner_owner_node
                )

                :telemetry.execute([:omegle_phoenix, :room, :message_sent], %{count: 1}, %{
                  session_id: session_id
                })

                {:noreply, socket}
              else
                Logger.debug(
                  "Swallowed in-flight message from #{session_id}: match state unavailable"
                )

                {:reply, {:ok, %{status: "ignored"}}, clear_match_assigns(socket)}
              end
            else
              {:reply, {:error, %{reason: "Invalid message content"}}, socket}
            end
          end
        end
      end
    end
  end

  def handle_in("message", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid message payload"}}, socket}
  end

  def handle_in("typing", %{"data" => data}, socket) when is_map(data) do
    Tracer.with_span "phoenix.room.typing", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.typing")
      session_id = socket.assigns[:session_id]
      partner_id = socket.assigns[:partner_id]
      match_generation = socket.assigns[:match_generation]
      partner_route = socket.assigns[:partner_route]
      partner_owner_node = socket.assigns[:partner_owner_node]

      Tracer.set_attributes(
        Map.merge(channel_span_attributes(socket), %{
          "room.partner_ref" => Tracing.safe_ref(partner_id || ""),
          "room.has_match_generation" => is_binary(match_generation)
        })
      )

      if is_nil(session_id) or is_nil(partner_id) do
        {:noreply, socket}
      else
        case Map.fetch(data, "typing") do
          {:ok, is_typing} when is_boolean(is_typing) ->
            Tracer.set_attribute("room.typing", is_typing)

            {socket, allowed} =
              if is_typing do
                check_typing_rate_limit(socket)
              else
                {socket, true}
              end

            if allowed do
              if is_binary(match_generation) and is_map(partner_route) do
                OmeglePhoenix.Router.send_message(
                  partner_id,
                  %{
                    type: "typing",
                    from: session_id,
                    match_generation: match_generation,
                    data: %{typing: is_typing}
                  },
                  route_hint: partner_route,
                  owner_hint: partner_owner_node
                )

                :telemetry.execute([:omegle_phoenix, :room, :typing_sent], %{count: 1}, %{
                  session_id: session_id,
                  typing: is_typing
                })
              end
            end

            {:noreply, socket}

          _ ->
            {:reply, {:error, %{reason: "Invalid typing payload"}}, socket}
        end
      end
    end
  end

  def handle_in("typing", _payload, socket) do
    {:reply, {:error, %{reason: "Invalid typing payload"}}, socket}
  end

  def handle_in("webrtc_ready", _payload, socket) do
    Tracer.with_span "phoenix.room.webrtc_ready", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.webrtc_ready")
      session_id = socket.assigns[:session_id]
      expected_partner_id = socket.assigns[:partner_id]
      expected_generation = socket.assigns[:match_generation]

      Tracer.set_attributes(
        Map.merge(channel_span_attributes(socket), %{
          "room.partner_ref" => Tracing.safe_ref(expected_partner_id || ""),
          "room.has_match_generation" => is_binary(expected_generation)
        })
      )

      case run_webrtc_ready(session_id) do
        {:ok, payload} ->
          Tracer.set_attribute("room.webrtc_ready.outcome", "ok")
          {:reply, {:ok, payload}, socket}

        {:error, %{reason: "Session busy, please retry"}} ->
          Tracer.set_attribute("room.webrtc_ready.outcome", "retry_scheduled")

          Logger.debug(
            "webrtc_ready lock contention for #{inspect(session_id)} with partner #{inspect(expected_partner_id)} generation #{inspect(expected_generation)}; scheduling async retry"
          )

          schedule_webrtc_ready_retry(session_id, expected_partner_id, expected_generation, 1)
          {:reply, {:ok, %{type: "webrtc_ready"}}, socket}

        {:error, payload} ->
          Tracer.set_attribute("room.webrtc_ready.outcome", "error")
          {:reply, {:error, payload}, socket}
      end
    end
  end

  def handle_in("stop", _payload, socket) do
    Tracer.with_span "phoenix.room.stop", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.stop")
      session_id = socket.assigns[:session_id]
      Tracer.set_attributes(channel_span_attributes(socket))

      case with_session_partner_lock(session_id, fn session ->
             cond do
               is_nil(session) ->
                 Tracer.set_attribute("room.stop.state", "missing_session")
                 {:ok, %{type: "stopped"}}

               is_nil(session.partner_id) ->
                 Tracer.set_attribute("room.stop.state", "queue_only")

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
                 Tracer.set_attributes(%{
                   "room.stop.state" => "matched",
                   "room.partner_ref" => Tracing.safe_ref(session.partner_id || "")
                 })

                 reset_match(session_id, session.partner_id, "partner cancelled")

                 :telemetry.execute([:omegle_phoenix, :room, :stopped], %{count: 1}, %{
                   session_id: session_id
                 })

                 {:ok, %{type: "stopped"}}
             end
           end) do
        {:ok, payload} ->
          {:reply, {:ok, payload}, clear_match_assigns(socket)}

        {:error, payload} ->
          Tracer.set_attribute("room.stop.state", "error")
          {:reply, {:error, payload}, socket}
      end
    end
  end

  def handle_in("disconnect", _payload, socket) do
    Tracer.with_span "phoenix.room.disconnect", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.disconnect")
      session_id = socket.assigns[:session_id]
      Tracer.set_attributes(channel_span_attributes(socket))

      case close_session(session_id, "partner disconnected") do
        :ok ->
          Tracer.set_attribute("room.disconnect.outcome", "ok")
          {:reply, {:ok, %{}}, clear_session_assigns(socket)}

        {:error, :not_found} ->
          Tracer.set_attribute("room.disconnect.outcome", "not_found")
          {:reply, {:error, %{reason: "Session not found"}}, socket}

        {:error, %{reason: reason}} ->
          Tracer.set_attributes(%{
            "room.disconnect.outcome" => "error",
            "room.disconnect.reason" => reason
          })

          {:reply, {:error, %{reason: reason}}, socket}
      end
    end
  end

  def handle_in("ping", _payload, socket) do
    Tracer.with_span "phoenix.room.ping", %{kind: :server} do
      Tracing.annotate_server("phoenix.room.ping")
      Tracer.set_attributes(channel_span_attributes(socket))

      if session_id = socket.assigns[:session_id] do
        _ = OmeglePhoenix.SessionManager.refresh_session(session_id)
        _ = OmeglePhoenix.Router.refresh_owner(session_id, self())
        Tracer.set_attribute("room.ping.refreshed", true)
      else
        Tracer.set_attribute("room.ping.refreshed", false)
      end

      {:reply, {:ok, %{type: "pong"}}, socket}
    end
  end

  def handle_in(_event, _payload, socket) do
    {:noreply, socket}
  end

  defp channel_span_attributes(socket) do
    %{
      "room.mode" => socket.assigns[:mode] || "unknown",
      "room.session_ref" => Tracing.safe_ref(socket.assigns[:session_id] || ""),
      "client.ip_hash" => Tracing.safe_ref(socket.assigns[:client_ip] || "")
    }
  end

  defp interests_count(preferences) do
    preferences
    |> Map.get("interests", "")
    |> to_string()
    |> String.split(",", trim: true)
    |> length()
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

  # Shared CAPTCHA gate used by both "search" and "start" handlers.
  # If the socket is already verified, skip the Cloudflare API call.
  # Otherwise verify the token and persist the flag on success.
  defp with_captcha_verified(socket, data, callback) do
    if Map.get(socket.assigns, :captcha_verified, false) do
      callback.(socket)
    else
      client_ip = socket.assigns[:client_ip] || "unknown"
      token = Map.get(data, "token")

      if OmeglePhoenix.Turnstile.verify(token, client_ip) do
        socket = assign(socket, :captcha_verified, true)
        callback.(socket)
      else
        {:reply, {:error, %{reason: "Invalid CAPTCHA"}}, socket}
      end
    end
  end

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

  def handle_info(
        {:retry_webrtc_ready, session_id, expected_partner_id, expected_generation, attempt},
        socket
      ) do
    current_session_id = socket.assigns[:session_id]
    current_partner_id = socket.assigns[:partner_id]
    current_generation = socket.assigns[:match_generation]

    cond do
      current_session_id != session_id ->
        Logger.debug(
          "Skipping stale webrtc_ready retry for #{inspect(session_id)} attempt #{attempt}: socket session is now #{inspect(current_session_id)}"
        )

        {:noreply, socket}

      current_partner_id != expected_partner_id or current_generation != expected_generation ->
        Logger.debug(
          "Skipping stale webrtc_ready retry for #{inspect(session_id)} attempt #{attempt}: expected partner #{inspect(expected_partner_id)} generation #{inspect(expected_generation)}, current partner #{inspect(current_partner_id)} generation #{inspect(current_generation)}"
        )

        {:noreply, socket}

      true ->
        case run_webrtc_ready(session_id) do
          {:ok, _payload} ->
            Logger.debug(
              "webrtc_ready retry succeeded for #{inspect(session_id)} attempt #{attempt} with partner #{inspect(current_partner_id)} generation #{inspect(current_generation)}"
            )

            {:noreply, socket}

          {:error, %{reason: "Session busy, please retry"}} ->
            Logger.debug(
              "webrtc_ready retry lock contention for #{inspect(session_id)} attempt #{attempt} with partner #{inspect(current_partner_id)} generation #{inspect(current_generation)}"
            )

            schedule_webrtc_ready_retry(
              session_id,
              expected_partner_id,
              expected_generation,
              attempt + 1
            )

            {:noreply, socket}

          {:error, %{reason: "No partner"}} ->
            Logger.debug(
              "webrtc_ready retry resolved as no-op for #{inspect(session_id)} attempt #{attempt}: partner already gone"
            )

            {:noreply, socket}

          {:error, payload} ->
            Logger.warning(
              "webrtc_ready retry failed for #{inspect(session_id)} attempt #{attempt}: #{inspect(payload)}"
            )

            {:noreply, socket}
        end
    end
  end

  def handle_info(
        {:router_match, partner_session_id, common_interests, match_generation, partner_route,
         partner_owner_node},
        socket
      ) do
    push(socket, "match", %{peer_id: partner_session_id, common_interests: common_interests})

    {:noreply,
     socket
     |> assign(:partner_id, partner_session_id)
     |> assign(:match_generation, match_generation)
     |> assign(:partner_route, normalize_partner_route(partner_route))
     |> assign(:partner_owner_node, normalize_partner_owner_node(partner_owner_node))}
  end

  def handle_info(
        {:router_message, %{type: type, from: from, match_generation: generation} = payload},
        socket
      ) do
    if current_match_message?(socket, from, generation) do
      push(socket, type, Map.delete(payload, :type))
    end

    {:noreply, socket}
  end

  def handle_info({:router_message, %{type: type, from: from} = payload}, socket) do
    Logger.warning(
      "Dropping router message with missing match_generation for #{socket.assigns[:session_id]}: #{inspect(%{type: type, from: from, keys: Map.keys(payload)})}"
    )

    {:noreply, socket}
  end

  def handle_info({:router_message, %{type: type} = payload}, socket) do
    # Messages without a :from field (system messages like webrtc_start)
    # are always delivered.
    push(socket, type, Map.delete(payload, :type))
    {:noreply, socket}
  end

  def handle_info(
        {:router_message,
         %{"type" => type, "from" => from, "match_generation" => generation} = payload},
        socket
      ) do
    if current_match_message?(socket, from, generation) do
      push(socket, type, Map.delete(payload, "type"))
    end

    {:noreply, socket}
  end

  def handle_info({:router_message, %{"type" => type, "from" => from} = payload}, socket) do
    Logger.warning(
      "Dropping router message with missing match_generation for #{socket.assigns[:session_id]}: #{inspect(%{type: type, from: from, keys: Map.keys(payload)})}"
    )

    {:noreply, socket}
  end

  def handle_info({:router_message, %{"type" => type} = payload}, socket) do
    push(socket, type, Map.delete(payload, "type"))
    {:noreply, socket}
  end

  def handle_info({:router_disconnect, reason, nil}, socket) do
    push(socket, "disconnected", %{reason: reason})
    {:noreply, clear_match_assigns(socket)}
  end

  def handle_info({:router_disconnect, reason, match_generation}, socket) do
    if socket.assigns[:match_generation] == match_generation do
      push(socket, "disconnected", %{reason: reason})
      {:noreply, clear_match_assigns(socket)}
    else
      {:noreply, socket}
    end
  end

  def handle_info(:router_timeout, socket) do
    push(socket, "timeout", %{})
    {:noreply, clear_match_assigns(socket)}
  end

  def handle_info({:router_banned, reason}, socket) do
    push(socket, "banned", %{reason: reason})
    {:noreply, clear_match_assigns(socket)}
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

  defp run_webrtc_ready(session_id) do
    with_session_partner_lock(session_id, fn session ->
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
                # Combine signaling_ready + webrtc_started into a single
                # update per session to halve the Redis round-trips.
                _ =
                  OmeglePhoenix.SessionManager.update_session(session_id, %{
                    webrtc_started: true
                  })

                _ =
                  OmeglePhoenix.SessionManager.update_session(updated_session.partner_id, %{
                    webrtc_started: true
                  })

                Logger.debug(
                  "Starting WebRTC for #{inspect(session_id)} and #{inspect(updated_session.partner_id)}"
                )

                OmeglePhoenix.Router.send_message(session_id, %{
                  type: "webrtc_start",
                  peer_id: updated_session.partner_id
                })

                OmeglePhoenix.Router.send_message(updated_session.partner_id, %{
                  type: "webrtc_start",
                  peer_id: session_id
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
    end)
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
      OmeglePhoenix.Router.notify_disconnect(
        partner_id,
        disconnect_reason,
        partner_session.match_generation
      )

      :ok
    else
      error ->
        Logger.warning(
          "Failed to reset match for #{session_id} / #{partner_id}: #{inspect(error)}"
        )

        :ok
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
        Process.sleep(50)
        with_session_partner_lock(session_id, fun, attempts_left - 1)

      {:retry, _updated_partner_id} ->
        with_session_partner_lock(session_id, fun, attempts_left - 1)

      result ->
        result
    end
  end

  defp schedule_webrtc_ready_retry(_session_id, _partner_id, _generation, attempt)
       when attempt > @webrtc_ready_retry_attempts,
       do: :ok

  defp schedule_webrtc_ready_retry(session_id, partner_id, generation, attempt) do
    delay_ms = @webrtc_ready_retry_delay_ms * attempt

    Logger.debug(
      "Scheduling webrtc_ready retry for #{inspect(session_id)} attempt #{attempt} in #{delay_ms}ms with partner #{inspect(partner_id)} generation #{inspect(generation)}"
    )

    Process.send_after(
      self(),
      {:retry_webrtc_ready, session_id, partner_id, generation, attempt},
      delay_ms
    )
  end

  defp create_and_queue_session(socket, client_ip, preferences, connected_type) do
    session_id = UUID.uuid4()

    case OmeglePhoenix.SessionManager.create_session(session_id, client_ip, preferences) do
      {:ok, session} ->
        case OmeglePhoenix.Router.register(session_id, self()) do
          :ok ->
            case OmeglePhoenix.Matchmaker.join_queue(session_id, preferences) do
              :ok ->
                {:reply,
                 {:ok,
                  %{type: connected_type, session_id: session_id, session_token: session.token}},
                 socket
                 |> assign(:session_id, session_id)
                 |> clear_match_assigns()}

              {:error, _reason} ->
                OmeglePhoenix.Router.unregister(session_id)
                _ = OmeglePhoenix.SessionManager.delete_session(session_id)
                {:reply, {:error, %{reason: "Matchmaking unavailable"}}, socket}
            end

          {:error, _reason} ->
            _ = OmeglePhoenix.SessionManager.delete_session(session_id)
            {:reply, {:error, %{reason: "Session routing unavailable"}}, socket}
        end

      {:error, _reason} ->
        {:reply, {:error, %{reason: "Failed to create session"}}, socket}
    end
  end

  defp clear_session_assigns(socket) do
    socket
    |> assign(:session_id, nil)
    |> clear_match_assigns()
  end

  defp clear_match_assigns(socket) do
    socket
    |> assign(:partner_id, nil)
    |> assign(:match_generation, nil)
    |> assign(:partner_route, nil)
    |> assign(:partner_owner_node, nil)
  end

  defp current_match_message?(socket, from, generation) do
    from == socket.assigns[:partner_id] and generation == socket.assigns[:match_generation]
  end

  defp normalize_partner_route(%{mode: mode, shard: shard})
       when is_binary(mode) and is_integer(shard),
       do: %{mode: mode, shard: shard}

  defp normalize_partner_route(%{"mode" => mode, "shard" => shard})
       when is_binary(mode) and is_integer(shard),
       do: %{mode: mode, shard: shard}

  defp normalize_partner_route(%{"mode" => mode, "shard" => shard})
       when is_binary(mode) and is_binary(shard) do
    case Integer.parse(shard) do
      {parsed_shard, ""} -> %{mode: mode, shard: parsed_shard}
      _ -> nil
    end
  end

  defp normalize_partner_route(_route), do: nil

  defp normalize_partner_owner_node(owner_node)
       when is_binary(owner_node) and byte_size(owner_node) > 0,
       do: owner_node

  defp normalize_partner_owner_node(_owner_node), do: nil

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
