defmodule OmeglePhoenix.Router do
  @moduledoc """
  Cluster-aware session event routing backed by Redis owner coordination.
  """

  use GenServer
  require Logger

  @owner_table :omegle_phoenix_router_owners
  @owner_value_separator "|"
  @compare_delete_script """
  if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
  end
  return 0
  """

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def register(session_id, pid) when is_binary(session_id) and is_pid(pid) do
    if pid != self() do
      {:error, :must_register_from_owner_process}
    else
      token = owner_token()
      :ok = Phoenix.PubSub.subscribe(OmeglePhoenix.PubSub, topic(session_id))
      track_local_owner(session_id, pid, token)

      case persist_owner(session_id, build_local_owner_record(token)) do
        :ok ->
          :ok

        {:error, reason} ->
          Phoenix.PubSub.unsubscribe(OmeglePhoenix.PubSub, topic(session_id))
          untrack_local_owner(session_id, pid)
          {:error, reason}
      end
    end
  end

  def refresh_owner(session_id, pid) when is_binary(session_id) and is_pid(pid) do
    if pid != self() do
      {:error, :must_refresh_from_owner_process}
    else
      case local_owner(session_id, pid) do
        {^pid, token} ->
          persist_owner(session_id, build_local_owner_record(token))

        _ ->
          {:error, :not_owner}
      end
    end
  end

  def unregister(session_id) when is_binary(session_id) do
    pid = self()
    local_owner = local_owner(session_id, pid)
    Phoenix.PubSub.unsubscribe(OmeglePhoenix.PubSub, topic(session_id))
    untrack_local_owner(session_id, pid)

    case local_owner do
      {_pid, token} ->
        compare_and_delete_owner(session_id, build_local_owner_record(token))

      _ ->
        :ok
    end

    :ok
  end

  def send_message(session_id, message, opts \\ []) do
    route(session_id, {:router_message, message}, opts)
  end

  def notify_match(session_id, partner_session_id, common_interests \\ [], match_generation \\ nil, route_hint \\ nil) do
    route(session_id, {:router_match, partner_session_id, common_interests, match_generation, route_hint})
  end

  def notify_disconnect(session_id, reason) do
    Logger.info("Notifying disconnect for session: #{session_id}, reason: #{reason}")
    route(session_id, {:router_disconnect, reason})
  end

  def notify_timeout(session_id) do
    route(session_id, :router_timeout)
  end

  def notify_banned(session_id, reason) do
    route(session_id, {:router_banned, reason})
  end

  @impl true
  def init(_opts) do
    _ = ensure_owner_table()
    node_channel = node_channel(Atom.to_string(Node.self()))
    :ok = Phoenix.PubSub.subscribe(OmeglePhoenix.PubSub, node_channel)

    state = %{
      channel: node_channel,
      owners: %{},
      owner_refs: %{}
    }

    {:ok, state}
  end

  @impl true
  def handle_info({:router_remote, session_id, message}, state) do
    _ = dispatch_local_if_owned(session_id, message)
    {:noreply, state}
  end

  def handle_info({:DOWN, ref, :process, pid, _reason}, state) do
    case Map.pop(state.owner_refs, ref) do
      {{session_id, ^pid}, owner_refs} ->
        case Map.get(state.owners, session_id) do
          {^pid, ^ref, token} ->
            :ets.delete(@owner_table, session_id)
            compare_and_delete_owner(session_id, build_local_owner_record(token))

            {:noreply,
             %{state | owners: Map.delete(state.owners, session_id), owner_refs: owner_refs}}

          _ ->
            {:noreply, %{state | owner_refs: owner_refs}}
        end

      {nil, _owner_refs} ->
        {:noreply, state}
    end
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  @impl true
  def handle_cast({:track_owner, session_id, pid, token}, state) do
    {state, old_ref} = drop_owner(state, session_id)

    if old_ref != nil do
      Process.demonitor(old_ref, [:flush])
    end

    ref = Process.monitor(pid)
    :ets.insert(@owner_table, {session_id, pid, token})

    {:noreply,
     %{
       state
       | owners: Map.put(state.owners, session_id, {pid, ref, token}),
         owner_refs: Map.put(state.owner_refs, ref, {session_id, pid})
     }}
  end

  def handle_cast({:untrack_owner, session_id, pid}, state) do
    {state, ref} = drop_owner(state, session_id, pid)

    if ref != nil do
      Process.demonitor(ref, [:flush])
    end

    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state), do: :ok

  defp route(session_id, message, opts \\ []) do
    current_node = Atom.to_string(Node.self())

    case owner_record(session_id, opts) do
      {:ok, nil} ->
        if not dispatch_local_if_owned(session_id, message) do
          maybe_cleanup_stale_session(session_id)
        end

      {:ok, %{node: owner_node} = owner} when owner_node == current_node ->
        if not dispatch_local_if_owned(session_id, message) do
          Logger.warning("Router cleared stale local owner for #{session_id}")
          compare_and_delete_owner(session_id, owner)
          maybe_cleanup_stale_session(session_id)
        end

      {:ok, owner} ->
        dispatch_remote(session_id, owner, message)

      {:error, reason} ->
        Logger.warning("Router owner lookup failed for #{session_id}: #{inspect(reason)}")

        if not dispatch_local_if_owned(session_id, message) do
          maybe_cleanup_stale_session(session_id)
        end
    end
  end

  defp owner_record(session_id, opts) do
    with {:ok, owner_key} <- owner_key(session_id, opts) do
      case OmeglePhoenix.Redis.command(["GET", owner_key]) do
        {:ok, encoded_owner} when is_binary(encoded_owner) ->
          case decode_owner_record(encoded_owner) do
            {:ok, owner} ->
              {:ok, owner}

            :error ->
              _ = compare_and_delete_owner_value(session_id, encoded_owner)
              {:error, :invalid_owner}
          end

        other ->
          other
      end
    end
  end

  defp dispatch_local(session_id, message) do
    case local_owner_pid(session_id) do
      nil ->
        false

      pid ->
        if Process.alive?(pid) do
          Logger.debug(
            "Router dispatching directly to owner for session: #{session_id}, message: #{inspect(summarize_message(message))}"
          )

          send(pid, message)
          true
        else
          :ets.delete(@owner_table, session_id)
          false
        end
    end
  end

  defp dispatch_remote(session_id, %{node: owner_node} = owner, message) do
    owner_node_atom = String.to_atom(owner_node)

    cond do
      owner_node_atom == Node.self() ->
        dispatch_local_if_owned(session_id, message)

      owner_node_atom in Node.list() ->
        Phoenix.PubSub.broadcast(
          OmeglePhoenix.PubSub,
          node_channel(owner_node),
          {:router_remote, session_id, message}
        )

        :ok

      true ->
        Logger.warning(
          "Router remote delivery found no connected node #{owner_node}; clearing stale owner for #{session_id}"
        )

        compare_and_delete_owner(session_id, owner)
        dispatch_local_if_owned(session_id, message)
    end
  end

  defp dispatch_local_if_owned(session_id, message) do
    dispatch_local(session_id, message)
  end

  defp track_local_owner(session_id, pid, token) do
    _ = ensure_owner_table()
    :ets.insert(@owner_table, {session_id, pid, token})
    GenServer.cast(__MODULE__, {:track_owner, session_id, pid, token})
    :ok
  end

  defp untrack_local_owner(session_id, pid) do
    if :ets.whereis(@owner_table) != :undefined do
      case :ets.lookup(@owner_table, session_id) do
        [{^session_id, ^pid, _token}] -> :ets.delete(@owner_table, session_id)
        _ -> :ok
      end
    end

    GenServer.cast(__MODULE__, {:untrack_owner, session_id, pid})
    :ok
  end

  defp local_owner_pid(session_id) do
    if :ets.whereis(@owner_table) == :undefined do
      nil
    else
      case :ets.lookup(@owner_table, session_id) do
        [{^session_id, pid, _token}] when is_pid(pid) -> pid
        _ -> nil
      end
    end
  end

  defp local_owner(session_id, pid) do
    if :ets.whereis(@owner_table) == :undefined do
      nil
    else
      case :ets.lookup(@owner_table, session_id) do
        [{^session_id, ^pid, token}] when is_binary(token) -> {pid, token}
        _ -> nil
      end
    end
  end

  defp ensure_owner_table do
    case :ets.whereis(@owner_table) do
      :undefined -> :ets.new(@owner_table, [:named_table, :public, :set, read_concurrency: true])
      table -> table
    end
  end

  defp drop_owner(state, session_id) do
    case Map.pop(state.owners, session_id) do
      {{_pid, ref, _token}, owners} ->
        :ets.delete(@owner_table, session_id)
        {%{state | owners: owners, owner_refs: Map.delete(state.owner_refs, ref)}, ref}

      {nil, owners} ->
        {%{state | owners: owners}, nil}
    end
  end

  defp drop_owner(state, session_id, pid) do
    case Map.pop(state.owners, session_id) do
      {{^pid, ref, _token}, owners} ->
        :ets.delete(@owner_table, session_id)
        {%{state | owners: owners, owner_refs: Map.delete(state.owner_refs, ref)}, ref}

      {{_other_pid, _ref, _token} = owner_entry, owners} ->
        {%{state | owners: Map.put(owners, session_id, owner_entry)}, nil}

      {nil, owners} ->
        {%{state | owners: owners}, nil}
    end
  end

  defp node_channel(owner_node), do: "router:node:" <> owner_node
  defp topic(session_id), do: "session:" <> session_id

  defp compare_and_delete_owner(session_id, expected_owner) do
    compare_and_delete_owner_value(session_id, encode_owner_record(expected_owner))
  end

  defp compare_and_delete_owner_value(session_id, expected_owner_value)
       when is_binary(expected_owner_value) do
    with {:ok, owner_key} <- owner_key(session_id, []) do
      _ =
        OmeglePhoenix.Redis.command([
          "EVAL",
          @compare_delete_script,
          "1",
          owner_key,
          expected_owner_value
        ])
    end

    :ok
  end

  defp persist_owner(session_id, owner) do
    with {:ok, owner_key} <- owner_key(session_id, []) do
      case OmeglePhoenix.Redis.command([
             "SETEX",
             owner_key,
             Integer.to_string(OmeglePhoenix.Config.get_router_owner_ttl_seconds()),
             encode_owner_record(owner)
           ]) do
        {:ok, "OK"} ->
          :ok

        {:error, reason} = error ->
          Logger.warning("Router failed to persist owner for #{session_id}: #{inspect(reason)}")
          error

        other ->
          Logger.warning(
            "Router received unexpected owner persist result for #{session_id}: #{inspect(other)}"
          )

          {:error, :unexpected_result}
      end
    end
  end

  defp build_local_owner_record(token) when is_binary(token) do
    %{node: Atom.to_string(Node.self()), token: token}
  end

  defp owner_key(session_id, opts) do
    route_hint = Keyword.get(opts, :route_hint)

    with {:ok, route} <- route_for_owner_key(session_id, route_hint) do
      {:ok, OmeglePhoenix.RedisKeys.session_owner_key(session_id, route)}
    end
  end

  defp route_for_owner_key(_session_id, %{mode: mode, shard: shard}) do
    {:ok, %{mode: mode, shard: shard}}
  end

  defp route_for_owner_key(session_id, _route_hint) do
    OmeglePhoenix.RedisKeys.resolve_session_route(session_id, verify_exists: false)
  end

  defp maybe_cleanup_stale_session(session_id) do
    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:error, :not_found} -> :ok
      _ -> :ok
    end
  end

  defp summarize_message({:router_message, %{type: type, from: from} = message}) do
    %{
      type: type,
      from: from,
      match_generation: Map.get(message, :match_generation),
      data_keys: message |> Map.get(:data, %{}) |> Map.keys()
    }
  end

  defp summarize_message({:router_message, %{"type" => type, "from" => from} = message}) do
    %{
      type: type,
      from: from,
      match_generation: Map.get(message, "match_generation"),
      data_keys: message |> Map.get("data", %{}) |> Map.keys()
    }
  end

  defp summarize_message({:router_message, %{type: type} = message}) do
    %{type: type, keys: Map.keys(message)}
  end

  defp summarize_message({:router_message, %{"type" => type} = message}) do
    %{type: type, keys: Map.keys(message)}
  end

  defp summarize_message(other), do: other

  defp owner_token do
    Integer.to_string(System.unique_integer([:positive, :monotonic]))
  end

  defp encode_owner_record(%{node: node, token: token})
       when is_binary(node) and is_binary(token) do
    node <> @owner_value_separator <> token
  end

  defp decode_owner_record(encoded_owner) when is_binary(encoded_owner) do
    case String.split(encoded_owner, @owner_value_separator, parts: 2) do
      [node, token] when byte_size(node) > 0 and byte_size(token) > 0 ->
        {:ok, %{node: node, token: token}}

      _ ->
        :error
    end
  end
end
