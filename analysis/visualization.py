#!/usr/bin/env python3
"""Visualização dos resultados do simulador de cache multi-tenant.

Lê os CSVs gerados por cmd/aggregate (analysis/agg_*.csv) e produz:

    1. analysis/plot_hr_vs_capacity.png — Hit Ratio global × Capacidade,
       uma linha por política. Avalia hipóteses H1, H2.

    2. analysis/plot_cdf_hr_per_tenant.png — CDF do Hit Ratio per-tenant
       em capacidades selecionadas (5%, 20%, 50%). Avalia H4, H5, H6.

    3. analysis/plot_boxplot_interference.png — Boxplot da interferência
       por (política, capacidade). Avalia H3.

    4. analysis/plot_pct_improved.png — % tenants melhorados em HR ×
       capacidade, uma linha por política comparada vs No Partition.

    5. analysis/plot_hr_change_stacked.png — barras 100% empilhadas: para
       cada capacidade, a fração de tenants cujo HR melhorou / manteve /
       piorou em relação ao baseline No Partition. Uma faceta por política
       (Memshare, FairShare).

Dependências:
    pip install pandas matplotlib numpy

Uso:
    python3 analysis/visualization.py [--input analysis] [--output analysis]
"""
from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

try:
    import pandas as pd
    import matplotlib.pyplot as plt
    import numpy as np
except ImportError as e:
    print(f"Dependência faltando: {e}. Rode: pip install pandas matplotlib numpy")
    sys.exit(1)


POLICY_COLORS = {
    "no_partition": "tab:blue",
    "memshare": "tab:green",
    "fairshare": "tab:orange",
}
POLICY_LABELS = {
    "no_partition": "No Partition (LRU)",
    "memshare": "Memshare",
    "fairshare": "FairShare Max-Min",
}
POLICY_MARKERS = {
    "no_partition": "o",
    "memshare": "s",
    "fairshare": "^",
}


def plot_hr_vs_capacity(global_df: pd.DataFrame, out_path: Path) -> None:
    """Linhas: Hit Ratio global × capacidade, uma por política."""
    fig, ax = plt.subplots(figsize=(8, 5))
    for policy, df in global_df.groupby("policy"):
        df = df.sort_values("capacity_pct")
        ax.plot(
            df["capacity_pct"],
            df["global_hit_ratio"] * 100,
            label=POLICY_LABELS.get(policy, policy),
            color=POLICY_COLORS.get(policy),
            marker=POLICY_MARKERS.get(policy, "x"),
            linewidth=2,
            markersize=8,
        )
    ax.set_xlabel("Capacidade do cache (% do Footprint)")
    ax.set_ylabel("Hit Ratio global (%)")
    ax.set_title("Hit Ratio global × Capacidade por política")
    ax.grid(True, alpha=0.3)
    ax.legend(loc="lower right")
    fig.tight_layout()
    fig.savefig(out_path, dpi=150)
    plt.close(fig)
    print(f"  → {out_path}")


def plot_cdf_hr_per_tenant(per_tenant_df: pd.DataFrame, out_path: Path,
                            capacities: list[int] | None = None) -> None:
    """Subplots de CDF do HR per-tenant para capacidades selecionadas."""
    if capacities is None:
        # Pegar capacidades disponíveis: extremos + meio
        avail = sorted(per_tenant_df["capacity_pct"].unique())
        if len(avail) >= 3:
            capacities = [avail[0], avail[len(avail) // 2], avail[-1]]
        else:
            capacities = avail

    fig, axes = plt.subplots(1, len(capacities), figsize=(4 * len(capacities), 4), sharey=True)
    if len(capacities) == 1:
        axes = [axes]

    for ax, cap in zip(axes, capacities):
        sub = per_tenant_df[per_tenant_df["capacity_pct"] == cap]
        for policy, df in sub.groupby("policy"):
            hr = df["hit_ratio"].sort_values().to_numpy()
            if len(hr) == 0:
                continue
            cdf = np.arange(1, len(hr) + 1) / len(hr)
            ax.plot(
                hr * 100, cdf,
                label=POLICY_LABELS.get(policy, policy),
                color=POLICY_COLORS.get(policy),
                linewidth=2,
            )
        ax.set_title(f"Capacidade {cap}%")
        ax.set_xlabel("Hit Ratio por tenant (%)")
        ax.grid(True, alpha=0.3)
    axes[0].set_ylabel("CDF (fração de tenants)")
    axes[-1].legend(loc="lower right", fontsize=9)
    fig.suptitle("CDF do Hit Ratio per-tenant", y=1.02)
    fig.tight_layout()
    fig.savefig(out_path, dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"  → {out_path}")


def plot_boxplot_interference(per_tenant_df: pd.DataFrame, out_path: Path) -> None:
    """Boxplot da interferência por (política, capacidade)."""
    capacities = sorted(per_tenant_df["capacity_pct"].unique())
    policies = sorted(per_tenant_df["policy"].unique())

    fig, ax = plt.subplots(figsize=(10, 5))
    width = 0.8 / max(1, len(policies))
    positions_base = np.arange(len(capacities))

    for i, policy in enumerate(policies):
        offset = (i - (len(policies) - 1) / 2) * width
        data = []
        for cap in capacities:
            sub = per_tenant_df[
                (per_tenant_df["policy"] == policy) & (per_tenant_df["capacity_pct"] == cap)
            ]
            data.append(sub["interference"].to_numpy())
        bp = ax.boxplot(
            data,
            positions=positions_base + offset,
            widths=width * 0.85,
            patch_artist=True,
            showfliers=False,
        )
        color = POLICY_COLORS.get(policy, "tab:gray")
        for patch in bp["boxes"]:
            patch.set_facecolor(color)
            patch.set_alpha(0.6)

    ax.set_xticks(positions_base)
    ax.set_xticklabels([f"{c}%" for c in capacities])
    ax.set_xlabel("Capacidade do cache (% do Footprint)")
    ax.set_ylabel("Interferência sofrida (fração)")
    ax.set_title("Interferência per-tenant por política e capacidade")

    # Legenda manual
    handles = [plt.Rectangle((0, 0), 1, 1, fc=POLICY_COLORS.get(p, "gray"), alpha=0.6)
               for p in policies]
    labels = [POLICY_LABELS.get(p, p) for p in policies]
    ax.legend(handles, labels, loc="upper right")

    ax.grid(True, alpha=0.3, axis="y")
    fig.tight_layout()
    fig.savefig(out_path, dpi=150)
    plt.close(fig)
    print(f"  → {out_path}")


def plot_pct_improved(comparisons_df: pd.DataFrame, out_path: Path) -> None:
    """% tenants melhorados em HR vs baseline, por política × capacidade."""
    fig, axes = plt.subplots(1, 2, figsize=(12, 5))

    for policy, df in comparisons_df.groupby("policy"):
        df = df.sort_values("capacity_pct")
        axes[0].plot(
            df["capacity_pct"],
            df["pct_tenants_improved_hr"],
            label=POLICY_LABELS.get(policy, policy),
            color=POLICY_COLORS.get(policy),
            marker=POLICY_MARKERS.get(policy, "x"),
            linewidth=2,
            markersize=8,
        )
        axes[1].plot(
            df["capacity_pct"],
            df["pct_tenants_reduced_interference"],
            label=POLICY_LABELS.get(policy, policy),
            color=POLICY_COLORS.get(policy),
            marker=POLICY_MARKERS.get(policy, "x"),
            linewidth=2,
            markersize=8,
        )

    axes[0].set_xlabel("Capacidade do cache (% do Footprint)")
    axes[0].set_ylabel("% tenants com HR > baseline")
    axes[0].set_title("Tenants melhorados em Hit Ratio vs No Partition")
    axes[0].grid(True, alpha=0.3)
    axes[0].legend(loc="best")

    axes[1].set_xlabel("Capacidade do cache (% do Footprint)")
    axes[1].set_ylabel("% tenants com interferência reduzida")
    axes[1].set_title("Tenants com interferência reduzida vs No Partition")
    axes[1].grid(True, alpha=0.3)
    axes[1].legend(loc="best")

    fig.tight_layout()
    fig.savefig(out_path, dpi=150)
    plt.close(fig)
    print(f"  → {out_path}")


HR_CHANGE_COLORS = {
    "improved": "tab:green",
    "unchanged": "tab:gray",
    "worsened": "tab:red",
}
HR_CHANGE_LABELS = {
    "improved": "HR melhorou",
    "unchanged": "HR manteve",
    "worsened": "HR piorou",
}


def _hr_change_breakdown(per_tenant_df: pd.DataFrame, policy: str,
                         baseline: str = "no_partition"):
    """Para cada capacidade, classifica os tenants (presentes em AMBOS os
    cenários) em melhorou/manteve/piorou o HR vs. baseline.

    Retorna (capacities, improved_pct, unchanged_pct, worsened_pct), com as
    frações em porcentagem (somam 100 por capacidade).
    """
    caps = sorted(per_tenant_df["capacity_pct"].unique())
    out_caps, improved, unchanged, worsened = [], [], [], []
    for cap in caps:
        base = per_tenant_df[
            (per_tenant_df["policy"] == baseline) & (per_tenant_df["capacity_pct"] == cap)
        ].set_index("tenant")["hit_ratio"]
        pol = per_tenant_df[
            (per_tenant_df["policy"] == policy) & (per_tenant_df["capacity_pct"] == cap)
        ].set_index("tenant")["hit_ratio"]
        common = base.index.intersection(pol.index)
        if len(common) == 0:
            continue
        diff = pol.loc[common] - base.loc[common]
        n = len(common)
        out_caps.append(cap)
        improved.append((diff > 0).sum() / n * 100)
        worsened.append((diff < 0).sum() / n * 100)
        unchanged.append((diff == 0).sum() / n * 100)
    return out_caps, improved, unchanged, worsened


def plot_hr_change_stacked(per_tenant_df: pd.DataFrame, out_path: Path,
                           policies: list[str] | None = None) -> None:
    """Barras 100% empilhadas por capacidade: fração de tenants que melhoraram,
    mantiveram ou pioraram o HR vs. No Partition. Uma faceta por política."""
    if policies is None:
        policies = [p for p in ("memshare", "fairshare")
                    if p in per_tenant_df["policy"].unique()]
    if not policies:
        print("  (nenhuma política não-baseline encontrada — pulando stacked)")
        return

    fig, axes = plt.subplots(1, len(policies), figsize=(6 * len(policies), 5),
                             sharey=True)
    if len(policies) == 1:
        axes = [axes]

    for ax, policy in zip(axes, policies):
        caps, improved, unchanged, worsened = _hr_change_breakdown(per_tenant_df, policy)
        if not caps:
            ax.set_title(f"{POLICY_LABELS.get(policy, policy)} (sem dados)")
            continue
        x = np.arange(len(caps))
        improved = np.array(improved)
        unchanged = np.array(unchanged)
        worsened = np.array(worsened)

        ax.bar(x, improved, color=HR_CHANGE_COLORS["improved"],
               label=HR_CHANGE_LABELS["improved"], width=0.7)
        ax.bar(x, unchanged, bottom=improved, color=HR_CHANGE_COLORS["unchanged"],
               label=HR_CHANGE_LABELS["unchanged"], width=0.7)
        ax.bar(x, worsened, bottom=improved + unchanged,
               color=HR_CHANGE_COLORS["worsened"],
               label=HR_CHANGE_LABELS["worsened"], width=0.7)

        # Rótulos de porcentagem no centro de cada segmento (omite se < 4%).
        for i in range(len(caps)):
            segments = [
                (improved[i], improved[i] / 2),
                (unchanged[i], improved[i] + unchanged[i] / 2),
                (worsened[i], improved[i] + unchanged[i] + worsened[i] / 2),
            ]
            for value, center in segments:
                if value >= 4:
                    ax.text(i, center, f"{value:.0f}%", ha="center", va="center",
                            fontsize=8, color="white", fontweight="bold")

        ax.set_xticks(x)
        ax.set_xticklabels([f"{c}%" for c in caps])
        ax.set_xlabel("Capacidade do cache (% do Footprint)")
        ax.set_title(f"{POLICY_LABELS.get(policy, policy)} vs. No Partition")
        ax.set_ylim(0, 100)

    axes[0].set_ylabel("% de tenants")
    # Legenda única para toda a figura.
    handles, labels = axes[0].get_legend_handles_labels()
    fig.legend(handles, labels, loc="upper center", ncol=3,
               bbox_to_anchor=(0.5, 1.02))
    fig.suptitle("Mudança no Hit Ratio per-tenant vs. No Partition", y=1.08)
    fig.tight_layout()
    fig.savefig(out_path, dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"  → {out_path}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Gera visualizações dos resultados.")
    parser.add_argument("--input", default="analysis", help="diretório com agg_*.csv")
    parser.add_argument("--output", default="analysis", help="diretório de saída para os PNGs")
    args = parser.parse_args()

    input_dir = Path(args.input)
    output_dir = Path(args.output)
    output_dir.mkdir(parents=True, exist_ok=True)

    global_csv = input_dir / "agg_global.csv"
    per_tenant_csv = input_dir / "agg_per_tenant.csv"
    comparisons_csv = input_dir / "agg_comparisons.csv"

    for path in [global_csv, per_tenant_csv, comparisons_csv]:
        if not path.exists():
            print(f"erro: {path} não encontrado. Rode `go run ./cmd/aggregate` primeiro.")
            sys.exit(1)

    print(f"Lendo CSVs de {input_dir}...")
    global_df = pd.read_csv(global_csv)
    per_tenant_df = pd.read_csv(per_tenant_csv)
    comparisons_df = pd.read_csv(comparisons_csv)

    print(f"Cenários: {len(global_df)} | per-tenant rows: {len(per_tenant_df)}")
    print(f"\nGerando gráficos em {output_dir}/:")

    plot_hr_vs_capacity(global_df, output_dir / "plot_hr_vs_capacity.png")
    plot_cdf_hr_per_tenant(per_tenant_df, output_dir / "plot_cdf_hr_per_tenant.png")
    plot_boxplot_interference(per_tenant_df, output_dir / "plot_boxplot_interference.png")
    plot_hr_change_stacked(per_tenant_df, output_dir / "plot_hr_change_stacked.png")
    if not comparisons_df.empty:
        plot_pct_improved(comparisons_df, output_dir / "plot_pct_improved.png")
    else:
        print("  (agg_comparisons.csv vazio — pulando plot_pct_improved)")

    print("\nConcluído.")


if __name__ == "__main__":
    main()
