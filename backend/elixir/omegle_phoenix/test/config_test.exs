defmodule OmeglePhoenix.ConfigTest do
  use ExUnit.Case, async: false

  setup do
    tracked = [
      "MIX_ENV",
      "ADMIN_STREAM_GROUP",
      "HEALTH_DETAILS_ENABLED",
      "MATCH_EVENT_STREAM_GROUP",
      "ROUTER_OWNER_TTL_SECONDS",
      "STREAM_STALE_CONSUMER_IDLE_MS",
      "TURNSTILE_ALLOW_INSECURE_BYPASS",
      "TURNSTILE_SECRET_KEY"
    ]

    previous = Map.new(tracked, &{&1, System.get_env(&1)})

    on_exit(fn ->
      Enum.each(previous, fn
        {key, nil} -> System.delete_env(key)
        {key, value} -> System.put_env(key, value)
      end)
    end)

    :ok
  end

  test "health details default to disabled in prod" do
    System.put_env("MIX_ENV", "prod")
    System.delete_env("HEALTH_DETAILS_ENABLED")

    refute OmeglePhoenix.Config.health_details_enabled?()
  end

  test "health details can be explicitly enabled" do
    System.put_env("MIX_ENV", "prod")
    System.put_env("HEALTH_DETAILS_ENABLED", "true")

    assert OmeglePhoenix.Config.health_details_enabled?()
  end

  test "turnstile bypass defaults to disabled in prod" do
    System.put_env("MIX_ENV", "prod")
    System.delete_env("TURNSTILE_ALLOW_INSECURE_BYPASS")

    refute OmeglePhoenix.Config.turnstile_insecure_bypass_allowed?()
  end

  test "router owner ttl is clamped to a safe minimum" do
    System.put_env("ROUTER_OWNER_TTL_SECONDS", "1")

    assert OmeglePhoenix.Config.get_router_owner_ttl_seconds() == 5
  end

  test "stream group defaults are stable across nodes" do
    System.delete_env("MATCH_EVENT_STREAM_GROUP")

    assert OmeglePhoenix.Config.get_match_event_stream_group() == "matchmaking:workers"
    assert OmeglePhoenix.Config.get_admin_stream_group() == "admin:workers"
  end

  test "stale consumer cleanup idle threshold is clamped" do
    System.put_env("STREAM_STALE_CONSUMER_IDLE_MS", "1000")

    assert OmeglePhoenix.Config.get_stream_stale_consumer_idle_ms() == 60_000
  end

  test "turnstile verify fails closed when bypass is disabled and secret is missing" do
    System.put_env("MIX_ENV", "prod")
    System.put_env("TURNSTILE_ALLOW_INSECURE_BYPASS", "false")
    System.delete_env("TURNSTILE_SECRET_KEY")

    token = String.duplicate("a", 100)
    refute OmeglePhoenix.Turnstile.verify(token, "127.0.0.1")
  end
end
