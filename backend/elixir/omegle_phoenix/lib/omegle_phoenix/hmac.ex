defmodule OmeglePhoenix.HMAC do
  @moduledoc """
  HMAC signing module for secure communication with Go services
  """

  def sign_request(payload) do
    secret = OmeglePhoenix.Config.get_shared_secret()
    payload_json = Jason.encode!(payload)
    signature = :crypto.mac(:hmac, :sha256, secret, payload_json)
    Base.encode64(signature)
  end

  def get_signature(payload, secret) do
    payload_json = Jason.encode!(payload)
    signature = :crypto.mac(:hmac, :sha256, secret, payload_json)
    Base.encode64(signature)
  end

  def verify_signature(payload, signature, secret) do
    payload_json = Jason.encode!(payload)
    expected_signature = :crypto.mac(:hmac, :sha256, secret, payload_json)
    expected_b64 = Base.encode64(expected_signature)
    Plug.Crypto.secure_compare(expected_b64, signature)
  end
end
