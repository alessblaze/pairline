defmodule JSON do
  @moduledoc false

  defdelegate decode(data), to: Jason
  defdelegate decode!(data), to: Jason
  defdelegate encode(data), to: Jason
  defdelegate encode!(data), to: Jason
  defdelegate encode_to_iodata(data), to: Jason
  defdelegate encode_to_iodata!(data), to: Jason
end
