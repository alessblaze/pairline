defmodule OmeglePhoenix.HTTPClient do
  @moduledoc """
  HTTP client for communicating with Go services
  """

  @go_service_url "http://localhost:8082"

  def post(url, body) do
    post(url, [], body)
  end

  def post(url, headers, body) do
    body_json = Jason.encode!(body)
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
    nonce = UUID.uuid4()

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

    body_json = Jason.encode!(payload_map)
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
