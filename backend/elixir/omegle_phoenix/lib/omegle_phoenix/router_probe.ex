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

defmodule OmeglePhoenix.RouterProbe do
  @moduledoc false

  def start(session_id, notify_pid) when is_binary(session_id) and is_pid(notify_pid) do
    Task.start(fn -> run(session_id, notify_pid) end)
  end

  def run(session_id, notify_pid) when is_binary(session_id) and is_pid(notify_pid) do
    case OmeglePhoenix.Router.register(session_id, self()) do
      :ok ->
        send(notify_pid, {:router_probe_registered, session_id, node(), self()})
        loop(session_id, notify_pid)

      {:error, reason} ->
        send(notify_pid, {:router_probe_failed, session_id, node(), reason})
        :ok
    end
  end

  defp loop(session_id, notify_pid) do
    receive do
      {:router_probe_stop, reply_to} when is_pid(reply_to) ->
        :ok = OmeglePhoenix.Router.unregister(session_id)
        send(reply_to, {:router_probe_stopped, session_id, node()})

      {:router_message, payload} ->
        send(notify_pid, {:router_probe_message, session_id, payload, node()})
        loop(session_id, notify_pid)

      {:router_match, partner_session_id, common_interests, match_generation, route_hint,
       owner_hint} =
          message ->
        _ = message

        send(
          notify_pid,
          {:router_probe_match, session_id, partner_session_id, common_interests,
           match_generation, route_hint, owner_hint, node()}
        )

        loop(session_id, notify_pid)

      other ->
        send(notify_pid, {:router_probe_other, session_id, other, node()})
        loop(session_id, notify_pid)
    end
  end
end
