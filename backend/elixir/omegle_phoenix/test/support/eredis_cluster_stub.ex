defmodule EredisClusterStub do
  @table __MODULE__

  def reset do
    ensure_table()
    :ets.delete_all_objects(@table)
    :ok
  end

  def put(fun_name, fun) when fun_name in [:q, :qk, :qmn] and is_function(fun) do
    ensure_table()
    true = :ets.insert(@table, {fun_name, fun})
    :ok
  end

  def dispatch(fun_name, args) do
    ensure_table()

    case :ets.lookup(@table, fun_name) do
      [{^fun_name, fun}] -> apply(fun, args)
      [] -> raise "No stub configured for #{inspect(fun_name)} with args #{inspect(args)}"
    end
  end

  defp ensure_table do
    case :ets.whereis(@table) do
      :undefined -> :ets.new(@table, [:named_table, :public, :set])
      _table -> @table
    end
  end
end
