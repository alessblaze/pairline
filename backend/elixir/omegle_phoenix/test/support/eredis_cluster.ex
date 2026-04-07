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

previous_ignore_module_conflict = Code.get_compiler_option(:ignore_module_conflict)
Code.put_compiler_option(:ignore_module_conflict, true)

defmodule :eredis_cluster do
  def q(cluster, command), do: EredisClusterStub.dispatch(:q, [cluster, command])
  def qk(cluster, command, key), do: EredisClusterStub.dispatch(:qk, [cluster, command, key])
  def qmn(cluster, commands), do: EredisClusterStub.dispatch(:qmn, [cluster, commands])

  def connect(_cluster, _init_nodes, _options), do: :ok
  def disconnect(_cluster), do: :ok
end

Code.put_compiler_option(:ignore_module_conflict, previous_ignore_module_conflict)
