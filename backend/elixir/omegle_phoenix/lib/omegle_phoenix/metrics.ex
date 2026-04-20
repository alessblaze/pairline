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

defmodule OmeglePhoenix.Metrics do
  @moduledoc """
  Lightweight in-memory counters populated from app telemetry events.
  """

  use GenServer

  @table :omegle_phoenix_metrics
  @handler_id "omegle-phoenix-metrics"
  @events [
    [:omegle_phoenix, :bots, :worker_started],
    [:omegle_phoenix, :bots, :message_enqueued],
    [:omegle_phoenix, :bots, :generation_started],
    [:omegle_phoenix, :bots, :generation_failed],
    [:omegle_phoenix, :bots, :reply_sent],
    [:omegle_phoenix, :bots, :conversation_finished],
    [:omegle_phoenix, :session_lock, :acquired],
    [:omegle_phoenix, :session_lock, :contended],
    [:omegle_phoenix, :redis_state, :success],
    [:omegle_phoenix, :redis_state, :failure],
    [:omegle_phoenix, :matchmaking, :queued],
    [:omegle_phoenix, :matchmaking, :timeout],
    [:omegle_phoenix, :matchmaking, :matched],
    [:omegle_phoenix, :matchmaking, :matched_local],
    [:omegle_phoenix, :matchmaking, :matched_overflow],
    [:omegle_phoenix, :matchmaking, :overflow_attempt],
    [:omegle_phoenix, :reaper, :orphaned_session],
    [:omegle_phoenix, :reaper, :queue_entry_removed],
    [:omegle_phoenix, :room, :message_sent],
    [:omegle_phoenix, :room, :typing_sent],
    [:omegle_phoenix, :room, :webrtc_ready],
    [:omegle_phoenix, :room, :webrtc_started],
    [:omegle_phoenix, :room, :skipped],
    [:omegle_phoenix, :room, :stopped],
    [:omegle_phoenix, :room, :disconnected]
  ]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def snapshot do
    case :ets.whereis(@table) do
      :undefined -> %{}
      table -> :ets.tab2list(table) |> Map.new()
    end
  end

  @impl true
  def init(_opts) do
    :ets.new(@table, [:named_table, :public, :set, read_concurrency: true])

    :telemetry.attach_many(@handler_id, @events, &__MODULE__.handle_event/4, nil)

    {:ok, %{}}
  end

  @impl true
  def terminate(_reason, _state) do
    :telemetry.detach(@handler_id)
    :ok
  end

  def handle_event(event, measurements, _metadata, _config) do
    key = event |> Enum.drop(2) |> Enum.join(".")
    increment(key, measurement_value(measurements))
  end

  defp measurement_value(measurements) do
    cond do
      is_integer(measurements[:count]) -> measurements[:count]
      is_integer(measurements[:duration]) -> 1
      true -> 1
    end
  end

  defp increment(key, amount) do
    :ets.update_counter(@table, key, {2, amount}, {key, 0})
  end
end
