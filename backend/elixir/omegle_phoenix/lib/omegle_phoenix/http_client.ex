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

defmodule OmeglePhoenix.HTTPClient do
  @moduledoc """
  HTTP client for communicating with Go services
  """

  @go_service_url "http://localhost:8082"

  def post(url, body) do
    post(url, [], body)
  end

  def post(url, headers, body) do
    body_json = JSON.encode!(body)
    all_headers = [{"content-type", "application/json"} | headers]

    request = Finch.build(:post, @go_service_url <> url, all_headers, body_json)

    case Finch.request(request, OmeglePhoenixFinch) do
      {:ok, response} ->
        {:ok, response.status, response.headers, response.body}

      {:error, reason} ->
        {:error, reason}
    end
  end

  def http_post_signed(url, payload) do
    http_post_signed(url, payload, 5000)
  end

  def http_post_signed(url, payload, timeout) do
    timestamp = System.system_time(:millisecond)
    nonce = Uniq.UUID.uuid4()

    payload_map = %{
      timestamp: timestamp,
      nonce: nonce,
      data: payload
    }

    signature = OmeglePhoenix.HMAC.sign_request(payload_map)

    headers = [
      {"x-signature", signature},
      {"x-timestamp", to_string(timestamp)},
      {"x-nonce", nonce}
    ]

    body_json = JSON.encode!(payload_map)
    all_headers = [{"content-type", "application/json"} | headers]

    request =
      Finch.build(:post, @go_service_url <> url, all_headers, body_json)

    case Finch.request(request, OmeglePhoenixFinch, receive_timeout: timeout) do
      {:ok, response} ->
        {:ok, response.status, response.headers, response.body}

      {:error, reason} ->
        {:error, reason}
    end
  end
end
