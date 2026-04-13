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

defmodule OmeglePhoenix.Redis.Streams do
  @moduledoc false

  def ensure_group(stream, group) do
    case OmeglePhoenix.Redis.command(["XGROUP", "CREATE", stream, group, "$", "MKSTREAM"]) do
      {:ok, "OK"} ->
        :ok

      {:error, reason} ->
        if busygroup_error?(reason) do
          :ok
        else
          {:error, reason}
        end
    end
  end

  def claim_stale_pending(stream, group, consumer) do
    do_claim_stale_pending(stream, group, consumer, "0-0", 0)
  end

  def cleanup_stale_consumers(stream, group, current_consumer, active_consumers, idle_cutoff_ms) do
    case OmeglePhoenix.Redis.command(["XINFO", "CONSUMERS", stream, group]) do
      {:ok, consumers} when is_list(consumers) ->
        Enum.each(consumers, fn consumer_info ->
          info = xinfo_to_map(consumer_info)
          name = Map.get(info, "name")
          idle_ms = xinfo_integer(info, "idle")

          if is_binary(name) and name != current_consumer and idle_ms >= idle_cutoff_ms and
               not MapSet.member?(active_consumers, name) do
            _ = OmeglePhoenix.Redis.command(["XGROUP", "DELCONSUMER", stream, group, name])
          end
        end)

      _ ->
        :ok
    end
  end

  def read_group(stream, group, consumer, batch_size, block_ms, stream_id) do
    command = [
      "XREADGROUP",
      "GROUP",
      group,
      consumer,
      "COUNT",
      Integer.to_string(batch_size),
      "BLOCK",
      Integer.to_string(block_ms),
      "STREAMS",
      stream,
      stream_id
    ]

    case OmeglePhoenix.Redis.command(command, timeout: block_ms + 30_000) do
      {:ok, nil} ->
        {:ok, []}

      {:ok, [[_stream, entries]]} when is_list(entries) ->
        {:ok, entries}

      {:ok, _other} ->
        {:ok, []}

      {:error, reason} ->
        {:error, reason}
    end
  end

  def ack_entries(_stream, _group, []), do: :ok

  def ack_entries(stream, group, entries) do
    ids = Enum.map(entries, fn [entry_id, _fields] -> entry_id end)

    case OmeglePhoenix.Redis.command(["XACK", stream, group | ids]) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  defp do_claim_stale_pending(_stream, _group, _consumer, _start_id, attempts)
       when attempts >= 100 do
    :ok
  end

  defp do_claim_stale_pending(stream, group, consumer, start_id, attempts) do
    case OmeglePhoenix.Redis.command([
           "XAUTOCLAIM",
           stream,
           group,
           consumer,
           "30000",
           start_id,
           "COUNT",
           "100"
         ]) do
      {:ok, [next_start_id, entries]} when is_binary(next_start_id) and is_list(entries) ->
        if entries == [] or next_start_id == start_id do
          :ok
        else
          do_claim_stale_pending(stream, group, consumer, next_start_id, attempts + 1)
        end

      {:ok, [next_start_id, entries, _deleted_ids]}
      when is_binary(next_start_id) and is_list(entries) ->
        if entries == [] or next_start_id == start_id do
          :ok
        else
          do_claim_stale_pending(stream, group, consumer, next_start_id, attempts + 1)
        end

      {:error, _} ->
        :ok

      _ ->
        :ok
    end
  end

  defp busygroup_error?(reason) do
    case redis_error_message(reason) do
      message when is_binary(message) -> String.starts_with?(message, "BUSYGROUP")
      _ -> false
    end
  end

  defp redis_error_message(%{message: message}) when is_binary(message), do: message
  defp redis_error_message(%{"message" => message}) when is_binary(message), do: message
  defp redis_error_message(message) when is_binary(message), do: message
  defp redis_error_message(message) when is_list(message), do: List.to_string(message)
  defp redis_error_message(_message), do: nil

  defp xinfo_to_map(list) when is_list(list) do
    cond do
      Enum.all?(list, &match?([_, _], &1)) ->
        Map.new(list)

      rem(length(list), 2) == 0 ->
        list
        |> Enum.chunk_every(2)
        |> Enum.reduce(%{}, fn
          [key, value], acc when is_binary(key) -> Map.put(acc, key, value)
          _, acc -> acc
        end)

      true ->
        %{}
    end
  end

  defp xinfo_to_map(_), do: %{}

  defp xinfo_integer(info, key) do
    case Map.get(info, key) do
      value when is_integer(value) ->
        value

      value when is_binary(value) ->
        case Integer.parse(value) do
          {parsed, _} -> parsed
          :error -> 0
        end

      _ ->
        0
    end
  end
end
