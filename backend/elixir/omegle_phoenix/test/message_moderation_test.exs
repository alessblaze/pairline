live_redis_enabled? =
  System.get_env("LIVE_REDIS_CLUSTER_TESTS") in ["1", "true", "TRUE", "yes", "on"]

if live_redis_enabled? do
  defmodule OmeglePhoenix.MessageModerationTest do
    use ExUnit.Case, async: false

    @moduletag skip: "stubbed Redis unit tests are disabled during live Redis integration runs"
  end
else
  defmodule OmeglePhoenix.MessageModerationTest do
    use ExUnit.Case, async: false

    setup do
      :persistent_term.put({OmeglePhoenix.MessageModeration, :words}, ["bad phrase"])
      :persistent_term.put({OmeglePhoenix.MessageModeration, :enabled}, true)

      on_exit(fn ->
        :persistent_term.put({OmeglePhoenix.MessageModeration, :words}, [])
        :persistent_term.put({OmeglePhoenix.MessageModeration, :enabled}, true)
      end)

      :ok
    end

    test "blocked_word short-circuits when enforcement is disabled" do
      :persistent_term.put({OmeglePhoenix.MessageModeration, :enabled}, false)

      assert OmeglePhoenix.MessageModeration.blocked_word("this contains bad phrase") ==
               {:ok, nil}
    end

    test "blocked_word matches phrases when enforcement is enabled" do
      assert OmeglePhoenix.MessageModeration.blocked_word("this contains bad phrase") ==
               {:ok, "bad phrase"}
    end
  end
end
