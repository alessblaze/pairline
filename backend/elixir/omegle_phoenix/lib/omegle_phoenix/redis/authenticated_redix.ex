defmodule OmeglePhoenix.Redis.AuthenticatedRedix do
  @moduledoc false

  def start_link(opts) do
    Redix.start_link(apply_auth(opts))
  end

  def command(conn, command, opts \\ []) do
    Redix.command(conn, command, opts)
  end

  def pipeline(conn, commands, opts \\ []) do
    Redix.pipeline(conn, commands, opts)
  end

  defp apply_auth(opts) do
    case OmeglePhoenix.Config.get_redis_password() do
      nil -> opts
      password -> Keyword.put_new(opts, :password, password)
    end
  end
end
