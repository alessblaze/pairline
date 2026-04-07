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
