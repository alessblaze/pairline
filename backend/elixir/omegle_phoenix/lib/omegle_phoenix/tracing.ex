defmodule OmeglePhoenix.Tracing do
  require OpenTelemetry.Tracer, as: Tracer

  def annotate_server(operation, attrs \\ %{}) do
    annotate("server", operation, attrs)
  end

  def annotate_internal(operation, attrs \\ %{}) do
    annotate("internal", operation, attrs)
  end

  def annotate_client(operation, attrs \\ %{}) do
    annotate("client", operation, attrs)
  end

  def safe_ref(value) when is_binary(value) and value != "" do
    value
    |> then(&:crypto.hash(:sha256, &1))
    |> Base.encode16(case: :lower)
    |> binary_part(0, 12)
  end

  def safe_ref(_value), do: ""

  defp annotate(layer, operation, attrs) when is_map(attrs) do
    Tracer.set_attributes(
      Map.merge(
        %{
          "pairline.span.layer" => layer,
          "pairline.operation.name" => operation
        },
        attrs
      )
    )
  end
end
