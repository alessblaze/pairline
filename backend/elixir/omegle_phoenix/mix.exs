defmodule OmeglePhoenix.MixProject do
  use Mix.Project

  def project do
    [
      app: :omegle_phoenix,
      version: "0.1.0",
      elixir: "~> 1.17",
      elixirc_paths: elixirc_paths(Mix.env()),
      start_permanent: Mix.env() == :prod,
      deps: deps()
    ]
  end

  # Run "mix help compile.app" to learn about applications.
  def application do
    [
      extra_applications: [:logger, :runtime_tools],
      mod: {OmeglePhoenix.Application, []}
    ]
  end

  # Run "mix help deps" to learn about dependencies.
  defp deps do
    [
      {:phoenix, "~> 1.7.14"},
      {:phoenix_pubsub, "~> 2.1"},
      {:phoenix_html, "~> 4.0"},
      {:phoenix_live_reload, "~> 1.4", only: :dev},
      {:phoenix_live_view, "~> 0.20"},
      {:plug_cowboy, "~> 2.6"},
      {:jason, "~> 1.4"},
      {:eredis_cluster, "~> 0.9"},
      {:castore, "~> 1.0"},
      {:finch, "~> 0.17"},
      {:uuid, "~> 1.1"},
      {:gettext, "~> 0.23"}
    ]
  end

  defp elixirc_paths(_), do: ["lib"]
end
