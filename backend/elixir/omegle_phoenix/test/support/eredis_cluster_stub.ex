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
