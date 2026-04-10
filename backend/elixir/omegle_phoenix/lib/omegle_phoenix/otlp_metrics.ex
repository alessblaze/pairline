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
      :erlang.system_flag(:scheduler_wall_time, true)
      send(self(), :export_metrics)
      {:ok, %{scheduler_sample: scheduler_sample()}}
    else
      :ignore
    end
  end

  @impl true
  def handle_info(:export_metrics, state) do
    {cpu_usage_percent, scheduler_sample} = scheduler_usage_percent(state.scheduler_sample)
    export_metrics(cpu_usage_percent)
    Process.send_after(self(), :export_metrics, @interval_ms)
    {:noreply, %{state | scheduler_sample: scheduler_sample}}
  end

  defp export_enabled? do
    endpoint()
    |> present?()
  end

  defp export_metrics(cpu_usage_percent) do
    with {:ok, endpoint} <- fetch_endpoint(),
         {:ok, request} <- build_request(cpu_usage_percent),
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

  defp build_request(cpu_usage_percent) do
    timestamp = System.system_time(:nanosecond)

    metrics =
      Enum.map(metric_defs(cpu_usage_percent), fn {name, description, unit, value} ->
        %{
          name: name,
          description: description,
          unit: unit,
          data: {:gauge,
           %{
             data_points: [
               %{
                 time_unix_nano: timestamp,
                 value: value
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
      string_attribute("service.name", "pairline-phoenix"),
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

  defp metric_defs(cpu_usage_percent) do
    [
      {"pairline.phoenix.runtime.memory.total_bytes", "Total BEAM memory", "By",
       {:as_int, :erlang.memory(:total)}},
      {"pairline.phoenix.runtime.memory.processes_bytes", "BEAM process memory", "By",
       {:as_int, :erlang.memory(:processes)}},
      {"pairline.phoenix.runtime.memory.processes_used_bytes", "BEAM process memory used", "By",
       {:as_int, :erlang.memory(:processes_used)}},
      {"pairline.phoenix.runtime.memory.system_bytes", "BEAM system memory", "By",
       {:as_int, :erlang.memory(:system)}},
      {"pairline.phoenix.runtime.memory.atom_bytes", "BEAM atom memory", "By",
       {:as_int, :erlang.memory(:atom)}},
      {"pairline.phoenix.runtime.memory.binary_bytes", "BEAM binary memory", "By",
       {:as_int, :erlang.memory(:binary)}},
      {"pairline.phoenix.runtime.memory.code_bytes", "BEAM code memory", "By",
       {:as_int, :erlang.memory(:code)}},
      {"pairline.phoenix.runtime.memory.ets_bytes", "BEAM ETS memory", "By",
       {:as_int, :erlang.memory(:ets)}},
      {"pairline.phoenix.runtime.process_count", "BEAM process count", "{process}",
       {:as_int, :erlang.system_info(:process_count)}},
      {"pairline.phoenix.runtime.port_count", "BEAM port count", "{port}",
       {:as_int, :erlang.system_info(:port_count)}},
      {"pairline.phoenix.runtime.cpu.scheduler_usage_percent", "BEAM scheduler CPU usage",
       "percent", {:as_double, cpu_usage_percent}}
    ]
  end

  defp scheduler_usage_percent(nil) do
    current = scheduler_sample()
    {0.0, current}
  end

  defp scheduler_usage_percent(previous) do
    current = scheduler_sample()
    active_delta = current.active - previous.active
    total_delta = current.total - previous.total

    usage =
      if total_delta > 0 and active_delta >= 0 do
        active_delta / total_delta * 100.0
      else
        0.0
      end

    {usage, current}
  end

  defp scheduler_sample do
    {active, total} =
      :erlang.statistics(:scheduler_wall_time)
      |> Enum.reduce({0, 0}, fn {_id, active, total}, {active_acc, total_acc} ->
        {active_acc + active, total_acc + total}
      end)

    %{active: active, total: total}
  end
end
