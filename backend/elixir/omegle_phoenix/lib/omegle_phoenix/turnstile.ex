defmodule OmeglePhoenix.Turnstile do
  require Logger

  @verify_url "https://challenges.cloudflare.com/turnstile/v0/siteverify"
  @max_token_size 2_048
  @min_token_size 100

  def verify(token, remoteip) when is_binary(token) and byte_size(token) >= @min_token_size and byte_size(token) <= @max_token_size do
    secret = System.get_env("TURNSTILE_SECRET_KEY")

    if is_nil(secret) or secret == "" do
      Logger.warning("TURNSTILE_SECRET_KEY is not set. Bypassing Turnstile verification.")
      true
    else
      body = URI.encode_query(%{
        "secret" => secret,
        "response" => token,
        "remoteip" => remoteip
      })

      request = Finch.build(:post, @verify_url, [{"content-type", "application/x-www-form-urlencoded"}], body)

      case Finch.request(request, OmeglePhoenixFinch, receive_timeout: 5000) do
        {:ok, %Finch.Response{status: status, body: resp_body}} when status in 200..299 ->
          case Jason.decode(resp_body) do
            {:ok, %{"success" => true}} ->
              true

            {:ok, %{"success" => false, "error-codes" => errors}} ->
              Logger.info("Turnstile verification failed: #{inspect(errors)}")
              false

            _ ->
              false
          end

        error ->
          Logger.error("Failed to connect to Turnstile API: #{inspect(error)}")
          false
      end
    end
  end

  def verify(_token, _remoteip), do: false
end
