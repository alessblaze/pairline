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

defmodule OmeglePhoenix.Redis.AdminSubscriber do
  use GenServer
  require Logger
  require OpenTelemetry.Tracer, as: Tracer

  alias OmeglePhoenix.Redis.Streams

  defstruct [:stream, :group, :consumer]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    stream = OmeglePhoenix.Config.get_admin_stream()

    state = %{
      stream: stream,
      group: OmeglePhoenix.Config.get_admin_stream_group(),
      consumer: stream_consumer_name()
    }

    send(self(), :connect)
    {:ok, state}
  end

  @impl true
  def handle_info(:connect, state) do
    case Streams.ensure_group(state.stream, state.group) do
      :ok ->
        claim_stale_pending(state.stream, state.group, state.consumer)
        cleanup_stale_consumers(state.stream, state.group, state.consumer)
        send(self(), :consume_stream)
        {:noreply, state}

      {:error, reason} ->
        Logger.error(
          "Failed to initialize admin stream #{state.stream} / #{state.group}: #{inspect(reason)}"
        )

        Process.send_after(self(), :connect, 1_000)
        {:noreply, state}
    end
  end

  def handle_info(:consume_stream, state) do
    case consume_stream_entries(state) do
      :ok ->
        send(self(), :consume_stream)
        {:noreply, state}

      {:error, reason} ->
        Logger.warning("Redis admin stream consumer disconnected: #{inspect(reason)}")
        Process.send_after(self(), :connect, 1_000)
        {:noreply, state}
    end
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state), do: :ok

  defp claim_stale_pending(stream, group, consumer) do
    Streams.claim_stale_pending(stream, group, consumer)
  end

  defp cleanup_stale_consumers(stream, group, current_consumer) do
    Streams.cleanup_stale_consumers(
      stream,
      group,
      current_consumer,
      active_consumer_names(current_consumer),
      OmeglePhoenix.Config.get_stream_stale_consumer_idle_ms()
    )
  end

  defp consume_stream_entries(state) do
    with :ok <- consume_pending_entries(state),
         {:ok, entries} <- read_stream(state, ">") do
      Enum.each(entries, &handle_stream_entry(state.stream, &1))
      _ = ack_stream_entries(state, entries)
      :ok
    end
  end

  defp consume_pending_entries(state) do
    case read_stream(state, "0") do
      {:ok, []} ->
        :ok

      {:ok, entries} ->
        Enum.each(entries, &handle_stream_entry(state.stream, &1))
        ack_stream_entries(state, entries)

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp read_stream(state, stream_id) do
    Streams.read_group(
      state.stream,
      state.group,
      state.consumer,
      OmeglePhoenix.Config.get_admin_stream_batch_size(),
      OmeglePhoenix.Config.get_admin_stream_block_ms(),
      stream_id
    )
  end

  defp ack_stream_entries(state, entries) do
    Streams.ack_entries(state.stream, state.group, entries)
  end

  defp handle_stream_entry(stream, [entry_id, fields]) when is_list(fields) do
    data =
      fields
      |> Enum.chunk_every(2)
      |> Enum.reduce(%{}, fn
        [key, value], acc -> Map.put(acc, key, value)
        _pair, acc -> acc
      end)

    case Map.get(data, "payload") do
      nil ->
        Logger.error("Invalid admin stream entry on #{stream}: #{inspect({entry_id, data})}")

      message ->
        case JSON.decode(message) do
          {:ok, %{"action" => action} = decoded} ->
            Tracer.with_span "admin.stream.action", %{kind: :server} do
              Tracer.set_attributes(%{"admin.action" => action})
              handle_admin_action(action, decoded)
            end

          _ ->
            Logger.error("Invalid admin stream payload: #{inspect(message)}")
        end
    end
  end

  defp handle_admin_action("emergency_ban", data) do
    session_id = Map.get(data, "session_id")
    reason = Map.get(data, "reason", "admin action")

    if session_id && uuid?(session_id) do
      OmeglePhoenix.SessionManager.emergency_ban(session_id, reason)
      Logger.info("Emergency ban: #{session_id} - #{reason}")
    else
      Logger.error("Emergency ban: missing or invalid session_id")
    end
  end

  defp handle_admin_action("emergency_ban_ip", data) do
    ip = Map.get(data, "ip")
    reason = Map.get(data, "reason", "admin action")

    if ip && valid_ip?(ip) do
      case OmeglePhoenix.SessionManager.emergency_ban_ip(ip, reason) do
        {:ok, _banned_sessions} ->
          Logger.info("Emergency ban IP: #{ip} - #{reason}")

        {:error, error_reason} ->
          Logger.error("Emergency ban IP failed for #{ip}: #{inspect(error_reason)}")
      end
    else
      Logger.error("Emergency ban IP: missing or invalid ip")
    end
  end

  defp handle_admin_action("emergency_disconnect", data) do
    session_id = Map.get(data, "session_id")

    if session_id && uuid?(session_id) do
      OmeglePhoenix.SessionManager.emergency_disconnect(session_id)
      Logger.info("Emergency disconnect: #{session_id}")
    else
      Logger.error("Emergency disconnect: missing or invalid session_id")
    end
  end

  defp handle_admin_action("emergency_unban", data) do
    session_id = Map.get(data, "session_id")

    if session_id && uuid?(session_id) do
      OmeglePhoenix.SessionManager.emergency_unban(session_id)
      Logger.info("Emergency unban: #{session_id}")
    else
      Logger.error("Emergency unban: missing or invalid session_id")
    end
  end

  defp handle_admin_action("emergency_unban_ip", data) do
    ip = Map.get(data, "ip")

    if ip && valid_ip?(ip) do
      case OmeglePhoenix.SessionManager.emergency_unban_ip(ip) do
        :ok ->
          Logger.info("Emergency unban IP: #{ip}")

        {:error, reason} ->
          Logger.error("Emergency unban IP failed for #{ip}: #{inspect(reason)}")
      end
    else
      Logger.error("Emergency unban IP: missing or invalid ip")
    end
  end

  defp handle_admin_action("server_shutdown", _data) do
    Logger.warning(
      "Server shutdown action received via Redis but rejected - not supported via pub/sub"
    )
  end

  defp handle_admin_action("refresh_banned_words", _data) do
    OmeglePhoenix.MessageModeration.refresh()
    Logger.info("Banned words cache refresh requested")
  end

  defp handle_admin_action(action, data) do
    Logger.warning("Unknown admin action: #{action}, data: #{inspect(data)}")
  end

  defp uuid?(str) when is_binary(str) do
    Regex.match?(
      ~r/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/,
      str
    )
  end

  defp uuid?(_), do: false

  defp valid_ip?(str) when is_binary(str) do
    case :inet.parse_address(String.to_charlist(str)) do
      {:ok, _} -> true
      _ -> false
    end
  end

  defp valid_ip?(_), do: false

  defp stream_consumer_name do
    sanitize_node_name(Node.self())
  end

  defp active_consumer_names(current_consumer) do
    [Node.self() | Node.list()]
    |> Enum.map(&sanitize_node_name/1)
    |> Enum.concat([current_consumer])
    |> MapSet.new()
  end

  defp sanitize_node_name(node) do
    node
    |> Atom.to_string()
    |> String.replace(~r/[^a-zA-Z0-9:_-]/u, "_")
  end
end
