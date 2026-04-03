defmodule OmeglePhoenixWeb.Endpoint do
  use Phoenix.Endpoint, otp_app: :omegle_phoenix

  socket("/ws", OmeglePhoenixWeb.Socket,
    websocket: [
      connect_info: [:peer_data, :x_headers],
      max_frame_size: 65_536
    ],
    longpolling: false
  )

  plug(Plug.Static,
    at: "/",
    from: :omegle_phoenix,
    gzip: false,
    only: ~w(assets fonts images favicon.ico robots.txt)
  )

  if code_reloading? do
    socket("/phoenix/live_reload/socket", Phoenix.LiveReloader.Socket)
    plug(Phoenix.LiveReloader)
    plug(Phoenix.CodeReloader)
  end

  plug(Plug.RequestId)
  plug(Plug.Telemetry, event_prefix: [:omegle_phoenix, :endpoint])

  plug(Plug.Parsers,
    parsers: [:urlencoded, :multipart, :json],
    pass: ["*/*"],
    json_decoder: Phoenix.json_library(),
    length: 2_097_152
  )

  plug(Plug.MethodOverride)
  plug(Plug.Head)

  plug :security_headers
  plug(OmeglePhoenixWeb.Router)

  defp security_headers(conn, _opts) do
    conn
    |> Plug.Conn.put_resp_header("x-content-type-options", "nosniff")
    |> Plug.Conn.put_resp_header("x-frame-options", "DENY")
  end
end
