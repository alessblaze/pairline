defmodule OmeglePhoenixWeb.Socket do
  use Phoenix.Socket
  import Bitwise

  @trusted_proxy_env "TRUSTED_PROXY_CIDRS"

  channel("room:*", OmeglePhoenixWeb.RoomChannel)

  @impl true
  def connect(_params, socket, connect_info) do
    client_ip = extract_ip(connect_info)
    {:ok, assign(socket, :client_ip, client_ip)}
  end

  defp extract_ip(connect_info) do
    peer_ip =
      case Map.get(connect_info, :peer_data) do
        %{address: address} -> normalize_ip(:inet.ntoa(address) |> to_string()) || "unknown"
        _ -> "unknown"
      end

    x_headers = Map.get(connect_info, :x_headers, [])

    forwarded_ip =
      get_header(x_headers, "cf-connecting-ipv6", true) ||
      get_header(x_headers, "cf-connecting-ip", true) ||
        get_header(x_headers, "x-real-ip", true) ||
        get_forwarded_for_ip(x_headers)

    if forwarded_ip && trusted_proxy?(peer_ip) do
      forwarded_ip
    else
      peer_ip
    end
  end

  defp trusted_proxy?(peer_ip) do
    trusted_proxy_ranges()
    |> Enum.any?(fn cidr -> cidr_match?(peer_ip, cidr) end)
  end

  defp trusted_proxy_ranges do
    case System.get_env(@trusted_proxy_env) do
      nil -> ["127.0.0.1/32", "::1/128"]
      "" -> ["127.0.0.1/32", "::1/128"]
      cidrs -> String.split(cidrs, ",") |> Enum.map(&String.trim/1)
    end
  end

  defp get_header(x_headers, header_name, normalize \\ false) do
    Enum.find_value(x_headers, fn {name, value} ->
      if String.downcase(name) == header_name do
        value
        |> String.trim()
        |> blank_to_nil()
        |> maybe_normalize_ip(normalize)
      end
    end)
  end

  defp get_forwarded_for_ip(x_headers) do
    case get_header(x_headers, "x-forwarded-for") do
      nil ->
        nil

      forwarded_for ->
        forwarded_for
        |> String.split(",")
        |> List.first()
        |> String.trim()
        |> blank_to_nil()
        |> normalize_ip()
    end
  end

  defp blank_to_nil(""), do: nil
  defp blank_to_nil(value), do: value

  defp maybe_normalize_ip(nil, _normalize), do: nil
  defp maybe_normalize_ip(value, false), do: value
  defp maybe_normalize_ip(value, true), do: normalize_ip(value)

  defp normalize_ip(nil), do: nil

  defp normalize_ip(value) do
    case :inet.parse_address(String.to_charlist(value)) do
      {:ok, address} -> address |> unmap_ipv4() |> :inet.ntoa() |> to_string()
      {:error, _reason} -> nil
    end
  end

  # Strip IPv4-in-IPv6 mapping (::ffff:A.B.C.D) so ban keys are always in
  # canonical IPv4 form, matching what the Go service stores via netip.Addr.Unmap().
  # Erlang represents ::ffff:A.B.C.D as {0,0,0,0,0,65535,AB,CD} where
  # AB = (A bsl 8) bor B and CD = (C bsl 8) bor D.
  defp unmap_ipv4({0, 0, 0, 0, 0, 65535, ab, cd}) do
    {ab >>> 8, ab &&& 0xFF, cd >>> 8, cd &&& 0xFF}
  end

  defp unmap_ipv4(addr), do: addr

  defp cidr_match?(peer_ip, cidr) do
    with {:ok, proxy} <- :inet.parse_address(String.to_charlist(peer_ip)),
         [network, bits] <- String.split(cidr, "/"),
         {:ok, network_addr} <- :inet.parse_address(String.to_charlist(network)),
         {prefix_length, ""} <- Integer.parse(bits) do
      match_cidr?(proxy, network_addr, prefix_length)
    else
      _ -> false
    end
  end

  # Serialize an IP address tuple to a binary for bitwise CIDR comparison.
  # IPv4 tuples are {a, b, c, d} — 4 elements of 1 byte each → 4-byte binary.
  # IPv6 tuples are {a, b, c, d, e, f, g, h} — 8 elements of 2 bytes each → 16-byte binary.
  # :erlang.list_to_binary/1 only works for byte values (0–255), so it cannot
  # be used directly for IPv6 (elements can be 0–65535).
  defp ip_to_binary(tuple) when tuple_size(tuple) == 4 do
    tuple |> Tuple.to_list() |> :erlang.list_to_binary()
  end

  defp ip_to_binary(tuple) when tuple_size(tuple) == 8 do
    for group <- Tuple.to_list(tuple), into: <<>>, do: <<group::16>>
  end

  defp match_cidr?(proxy, network, prefix_length) do
    proxy_bin = ip_to_binary(proxy)
    network_bin = ip_to_binary(network)
    bytes = div(prefix_length, 8)
    bits = rem(prefix_length, 8)

    case {byte_size(proxy_bin), byte_size(network_bin)} do
      {size, size} ->
        <<proxy_prefix::binary-size(bytes), proxy_rest::binary>> = proxy_bin
        <<network_prefix::binary-size(bytes), network_rest::binary>> = network_bin

        prefix_match = proxy_prefix == network_prefix

        bit_match =
          case bits do
            0 ->
              true

            _ ->
              <<proxy_next, _::binary>> = proxy_rest
              <<network_next, _::binary>> = network_rest
              mask = 0xFF <<< (8 - bits) &&& 0xFF
              (proxy_next &&& mask) == (network_next &&& mask)
          end

        prefix_match and bit_match

      _ ->
        false
    end
  end

  @impl true
  def id(_socket), do: nil
end
