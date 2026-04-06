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
