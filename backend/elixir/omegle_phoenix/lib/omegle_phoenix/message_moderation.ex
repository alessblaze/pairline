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

defmodule OmeglePhoenix.MessageModeration do
  @moduledoc false

  use GenServer
  require Logger

  @banned_words_key "moderation:banned_words"
  @banned_words_enabled_key "moderation:banned_words:enabled"
  @cache_key {__MODULE__, :words}
  @enabled_cache_key {__MODULE__, :enabled}
  @refresh_interval_ms 30_000

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def blocked_word(content) when is_binary(content) do
    if not :persistent_term.get(@enabled_cache_key, true) do
      {:ok, nil}
    else
      normalized_content = normalize(content)

      if normalized_content == "" do
        {:ok, nil}
      else
        content_tokens = String.split(normalized_content, " ", trim: true)

        blocked =
          @cache_key
          |> :persistent_term.get([])
          |> Enum.find(&phrase_matches?(content_tokens, &1))

        {:ok, blocked}
      end
    end
  end

  def blocked_word(_content), do: {:ok, nil}

  def refresh do
    GenServer.cast(__MODULE__, :refresh)
  end

  @impl true
  def init(_opts) do
    :persistent_term.put(@cache_key, [])
    :persistent_term.put(@enabled_cache_key, true)
    refreshed_state = refresh_cache(%{})
    schedule_refresh()
    {:ok, refreshed_state}
  end

  @impl true
  def handle_cast(:refresh, state) do
    {:noreply, refresh_cache(state)}
  end

  @impl true
  def handle_info(:refresh, state) do
    refreshed_state = refresh_cache(state)
    schedule_refresh()
    {:noreply, refreshed_state}
  end

  @impl true
  def handle_info(_message, state), do: {:noreply, state}

  defp refresh_cache(state) do
    case OmeglePhoenix.Redis.command(["GET", @banned_words_enabled_key]) do
      {:ok, enabled_value} ->
        enabled = enabled?(enabled_value)
        :persistent_term.put(@enabled_cache_key, enabled)

        if enabled do
          refresh_words_cache(state)
        else
          :persistent_term.put(@cache_key, [])
          state |> Map.put(:enabled, false) |> Map.put(:words_count, 0)
        end

      {:error, reason} ->
        Logger.warning("Failed to refresh banned words enabled flag: #{inspect(reason)}")
        state

      other ->
        Logger.warning("Unexpected banned words enabled flag result: #{inspect(other)}")
        state
    end
  end

  defp schedule_refresh do
    Process.send_after(self(), :refresh, @refresh_interval_ms)
  end

  defp normalize(value) when is_binary(value) do
    value
    |> String.downcase()
    |> String.split()
    |> Enum.map(&normalize_token/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.join(" ")
  end

  defp normalize(_value), do: ""

  defp refresh_words_cache(state) do
    case OmeglePhoenix.Redis.command(["SMEMBERS", @banned_words_key]) do
      {:ok, words} when is_list(words) ->
        normalized_words =
          words
          |> Enum.map(&normalize/1)
          |> Enum.reject(&(&1 == ""))
          |> Enum.uniq()
          |> Enum.sort()

        :persistent_term.put(@cache_key, normalized_words)
        state |> Map.put(:enabled, true) |> Map.put(:words_count, length(normalized_words))

      {:error, reason} ->
        Logger.warning("Failed to refresh banned words cache: #{inspect(reason)}")
        state

      other ->
        Logger.warning("Unexpected banned words cache refresh result: #{inspect(other)}")
        state
    end
  end

  defp enabled?(nil), do: true
  defp enabled?(:undefined), do: true

  defp enabled?(value) when is_binary(value) do
    case String.trim(String.downcase(value)) do
      "0" -> false
      "false" -> false
      "off" -> false
      "no" -> false
      "disabled" -> false
      _ -> true
    end
  end

  defp enabled?(_value), do: true

  defp normalize_token(token) when is_binary(token) do
    token
    |> String.replace(~r/^[^\p{L}\p{N}]+/u, "")
    |> String.replace(~r/[^\p{L}\p{N}]+$/u, "")
  end

  defp phrase_matches?(_content_tokens, ""), do: false

  defp phrase_matches?(content_tokens, phrase) do
    phrase_tokens = String.split(phrase, " ", trim: true)
    phrase_length = length(phrase_tokens)

    phrase_length > 0 and
      content_tokens
      |> Enum.chunk_every(phrase_length, 1, :discard)
      |> Enum.any?(&(&1 == phrase_tokens))
  end
end
