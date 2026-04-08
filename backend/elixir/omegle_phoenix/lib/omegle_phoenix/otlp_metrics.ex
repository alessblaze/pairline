defmodule OmeglePhoenix.OTLPMetrics do
  @moduledoc false

  use GenServer
  require Logger

  @interval_ms 15_000

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    if export_enabled?() do
      :inets.start()
      :ssl.start()
      send(self(), :export_metrics)
      {:ok, %{}}
    else
      :ignore
    end
  end

  @impl true
  def handle_info(:export_metrics, state) do
    export_metrics()
    Process.send_after(self(), :export_metrics, @interval_ms)
    {:noreply, state}
  end

  defp export_enabled? do
    endpoint()
    |> present?()
  end

  defp export_metrics do
    with {:ok, endpoint} <- fetch_endpoint(),
         {:ok, request} <- build_request(),
         body <- :opentelemetry_exporter_metrics_service_pb.encode_msg(request, :export_metrics_service_request),
         headers <- otlp_headers(),
         {:ok, {{_, status, _}, _, _}} when status in 200..202 <-
           :httpc.request(
             :post,
             {String.to_charlist(endpoint), headers, ~c"application/x-protobuf", body},
             http_options(endpoint),
             []
           ) do
      :ok
    else
      {:ok, {{_, status, _}, _, response_body}} ->
        Logger.warning(
          "Phoenix OTLP metrics export failed with status #{status}: #{inspect(response_body)}"
        )

      {:error, :missing_endpoint} ->
        :ok

      {:error, reason} ->
        Logger.warning("Phoenix OTLP metrics export failed: #{inspect(reason)}")
    end
  end

  defp build_request do
    timestamp = System.system_time(:nanosecond)

    metrics =
      Enum.map(metric_defs(), fn {name, description, unit, fun} ->
        %{
          name: name,
          description: description,
          unit: unit,
          data: {:gauge,
           %{
             data_points: [
               %{
                 time_unix_nano: timestamp,
                 value: {:as_int, fun.()}
               }
             ]
           }}
        }
      end)

    {:ok,
     %{
       resource_metrics: [
         %{
           resource: %{attributes: resource_attributes()},
           scope_metrics: [
             %{
               scope: %{name: "pairline/phoenix/runtime"},
               metrics: metrics
             }
           ]
         }
       ]
     }}
  end

  defp resource_attributes do
    [
      string_attribute("service.name", "omegle-phoenix"),
      string_attribute("service.instance.id", Atom.to_string(Node.self()))
    ]
    |> maybe_append("deployment.environment", System.get_env("OTEL_ENVIRONMENT"))
  end

  defp string_attribute(key, value) do
    %{key: key, value: %{value: {:string_value, value}}}
  end

  defp maybe_append(attrs, _key, nil), do: attrs
  defp maybe_append(attrs, _key, ""), do: attrs
  defp maybe_append(attrs, key, value), do: attrs ++ [string_attribute(key, value)]

  defp fetch_endpoint do
    case endpoint() do
      nil -> {:error, :missing_endpoint}
      value -> {:ok, String.trim(value)}
    end
  end

  defp endpoint do
    metrics_endpoint = System.get_env("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")

    cond do
      present?(metrics_endpoint) ->
        metrics_endpoint

      present?(System.get_env("OTEL_EXPORTER_OTLP_ENDPOINT")) ->
        append_metrics_path(System.get_env("OTEL_EXPORTER_OTLP_ENDPOINT"))

      true ->
        nil
    end
  end

  defp append_metrics_path(base) do
    base = String.trim_trailing(base, "/")

    if String.ends_with?(base, "/v1/metrics") do
      base
    else
      base <> "/v1/metrics"
    end
  end

  defp otlp_headers do
    parse_header_env("OTEL_EXPORTER_OTLP_HEADERS") ++
      parse_header_env("OTEL_EXPORTER_OTLP_METRICS_HEADERS")
  end

  defp parse_header_env(name) do
    name
    |> System.get_env("")
    |> String.split(",", trim: true)
    |> Enum.reduce([], fn entry, acc ->
      case String.split(entry, "=", parts: 2) do
        [key, value] ->
          [{String.trim(key) |> String.to_charlist(), String.trim(value) |> String.to_charlist()} | acc]

        _ ->
          acc
      end
    end)
    |> Enum.reverse()
  end

  defp http_options(endpoint) do
    common = [timeout: 5_000, connect_timeout: 2_000]

    if String.starts_with?(endpoint, "https://") do
      Keyword.put(common, :ssl, [{:verify, :verify_none}])
    else
      common
    end
  end

  defp present?(value), do: is_binary(value) and String.trim(value) != ""

  defp metric_defs do
    [
      {"pairline.phoenix.runtime.memory.total_bytes", "Total BEAM memory", "By",
       fn -> :erlang.memory(:total) end},
      {"pairline.phoenix.runtime.memory.processes_bytes", "BEAM process memory", "By",
       fn -> :erlang.memory(:processes) end},
      {"pairline.phoenix.runtime.memory.processes_used_bytes", "BEAM process memory used", "By",
       fn -> :erlang.memory(:processes_used) end},
      {"pairline.phoenix.runtime.memory.system_bytes", "BEAM system memory", "By",
       fn -> :erlang.memory(:system) end},
      {"pairline.phoenix.runtime.memory.atom_bytes", "BEAM atom memory", "By",
       fn -> :erlang.memory(:atom) end},
      {"pairline.phoenix.runtime.memory.binary_bytes", "BEAM binary memory", "By",
       fn -> :erlang.memory(:binary) end},
      {"pairline.phoenix.runtime.memory.code_bytes", "BEAM code memory", "By",
       fn -> :erlang.memory(:code) end},
      {"pairline.phoenix.runtime.memory.ets_bytes", "BEAM ETS memory", "By",
       fn -> :erlang.memory(:ets) end},
      {"pairline.phoenix.runtime.process_count", "BEAM process count", "{process}",
       fn -> :erlang.system_info(:process_count) end},
      {"pairline.phoenix.runtime.port_count", "BEAM port count", "{port}",
       fn -> :erlang.system_info(:port_count) end}
    ]
  end
end
