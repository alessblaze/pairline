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
