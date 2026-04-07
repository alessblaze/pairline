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

defmodule OmeglePhoenix.Turnstile do
  require Logger

  @verify_url "https://challenges.cloudflare.com/turnstile/v0/siteverify"
  @max_token_size 2_048
  @min_token_size 100

  def verify(token, remoteip)
      when is_binary(token) and byte_size(token) >= @min_token_size and
             byte_size(token) <= @max_token_size do
    secret = System.get_env("TURNSTILE_SECRET_KEY")

    if is_nil(secret) or secret == "" do
      if OmeglePhoenix.Config.turnstile_insecure_bypass_allowed?() do
        Logger.warning(
          "TURNSTILE_SECRET_KEY is not set. Allowing insecure Turnstile bypass outside hardened environments."
        )

        true
      else
        Logger.error(
          "TURNSTILE_SECRET_KEY is not set. Rejecting Turnstile verification because insecure bypass is disabled."
        )

        false
      end
    else
      body =
        URI.encode_query(%{
          "secret" => secret,
          "response" => token,
          "remoteip" => remoteip
        })

      request =
        Finch.build(
          :post,
          @verify_url,
          [{"content-type", "application/x-www-form-urlencoded"}],
          body
        )

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
