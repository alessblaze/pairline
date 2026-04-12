defmodule OmeglePhoenix.MessageModeration do
  @moduledoc false

  use GenServer
  require Logger

  @banned_words_key "moderation:banned_words"
  @cache_key {__MODULE__, :words}
  @refresh_interval_ms 30_000

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def blocked_word(content) when is_binary(content) do
    normalized_content = normalize(content)

    if normalized_content == "" do
      {:ok, nil}
    else
      blocked =
        @cache_key
        |> :persistent_term.get([])
        |> Enum.find(&String.contains?(normalized_content, &1))

      {:ok, blocked}
    end
  end

  def blocked_word(_content), do: {:ok, nil}

  def refresh do
    GenServer.cast(__MODULE__, :refresh)
  end

  @impl true
  def init(_opts) do
    :persistent_term.put(@cache_key, [])
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
    case OmeglePhoenix.Redis.command(["SMEMBERS", @banned_words_key]) do
      {:ok, words} when is_list(words) ->
        normalized_words =
          words
          |> Enum.map(&normalize/1)
          |> Enum.reject(&(&1 == ""))
          |> Enum.uniq()
          |> Enum.sort()

        :persistent_term.put(@cache_key, normalized_words)
        Map.put(state, :words_count, length(normalized_words))

      {:error, reason} ->
        Logger.warning("Failed to refresh banned words cache: #{inspect(reason)}")
        state

      other ->
        Logger.warning("Unexpected banned words cache refresh result: #{inspect(other)}")
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
    |> Enum.join(" ")
  end

  defp normalize(_value), do: ""
end
