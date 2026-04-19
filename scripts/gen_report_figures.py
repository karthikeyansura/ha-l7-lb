#!/usr/bin/env python3
"""Generate the three report figures from committed result data.

Outputs:
  - results/figures/hetero_distribution.png
  - results/figures/retry_flip.png
  - results/figures/scaling_data.png

Run: python3 scripts/gen_report_figures.py
"""
from __future__ import annotations

import csv
import json
from pathlib import Path

import matplotlib.pyplot as plt

ROOT = Path(__file__).resolve().parent.parent
RESULTS = ROOT / "results" / "final"
FIG_DIR = ROOT / "results" / "figures"
FIG_DIR.mkdir(parents=True, exist_ok=True)


def _aggregated_row(stats_csv: Path) -> dict:
    with stats_csv.open() as f:
        for r in csv.reader(f):
            if len(r) > 2 and r[1] == "Aggregated":
                return {
                    "reqs": int(r[2]),
                    "fails": int(r[3]),
                    "rps": float(r[9]),
                    "p99": r[17],
                }
    raise ValueError(f"no Aggregated row in {stats_csv}")


def _backend_split(metrics_json: Path) -> tuple[int, int]:
    """Return (strong_count, weak_count) from an LB /metrics JSON.

    Strong is assumed to be the backend with more requests.
    """
    d = json.loads(metrics_json.read_text())
    counts = sorted(
        (s["RequestCount"] for s in d["BackendStats"].values()), reverse=True
    )
    return counts[0], counts[1] if len(counts) > 1 else 0


# -----------------------------------------------------------------------------
# Figure 1: heterogeneous distribution across RR / LC / Weighted
# -----------------------------------------------------------------------------
def fig_hetero_distribution() -> None:
    algos = ["Round-Robin", "Least-Connections", "Weighted (70/30)"]
    runs = ["hetero_rr_stress_u20", "hetero_lc_stress_u20", "hetero_weighted_stress_u20"]

    strong_pct, weak_pct = [], []
    for run in runs:
        m = RESULTS / "exp1_robustness" / f"{run}_end" / "lb_snapshots" / "lb0_metrics.json"
        strong, weak = _backend_split(m)
        total = strong + weak
        strong_pct.append(strong / total * 100)
        weak_pct.append(weak / total * 100)

    fig, ax = plt.subplots(figsize=(8, 5))
    x = range(len(algos))
    bars_strong = ax.bar(x, strong_pct, 0.55, label="Strong (0.5 vCPU)", color="#3b7dd8")
    bars_weak = ax.bar(
        x, weak_pct, 0.55, bottom=strong_pct, label="Weak (0.25 vCPU)", color="#e27d60"
    )

    # Reference line at the target 70% weighted split
    ax.axhline(70, color="gray", linestyle="--", linewidth=1, alpha=0.6)
    ax.text(2.32, 71, "target 70%", fontsize=9, color="gray", ha="left")

    # Value labels on each segment
    for bars, pcts in [(bars_strong, strong_pct), (bars_weak, weak_pct)]:
        for bar, pct in zip(bars, pcts):
            ax.text(
                bar.get_x() + bar.get_width() / 2,
                bar.get_y() + bar.get_height() / 2,
                f"{pct:.1f}%",
                ha="center",
                va="center",
                color="white",
                fontweight="bold",
                fontsize=11,
            )

    ax.set_ylabel("Share of requests (%)")
    ax.set_xticks(list(x))
    ax.set_xticklabels(algos)
    ax.set_ylim(0, 100)
    ax.set_title(
        "Heterogeneous backend distribution under BackendStressUser\n"
        "(per-backend request counts from LB /metrics, u=20, 5-min run)"
    )
    ax.legend(loc="upper right")
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)
    fig.tight_layout()
    fig.savefig(FIG_DIR / "hetero_distribution.png", dpi=150)
    plt.close(fig)
    print("  ok: hetero_distribution.png")


# -----------------------------------------------------------------------------
# Figure 2: retry on/off inversion — Part A (broad chaos) vs Part B (replica drop)
# -----------------------------------------------------------------------------
def fig_retry_flip() -> None:
    part_a_on = _aggregated_row(
        RESULTS / "exp2a_low_chaos" / "retry_on_200" / "stats.csv"
    )
    part_a_off = _aggregated_row(
        RESULTS / "exp2a_low_chaos" / "retry_off_200" / "stats.csv"
    )
    part_b_on = _aggregated_row(
        RESULTS / "exp2b" / "retry_on_replicadrop" / "stats.csv"
    )
    part_b_off = _aggregated_row(
        RESULTS / "exp2b" / "retry_off_replicadrop" / "stats.csv"
    )

    def pct(r: dict) -> float:
        return r["fails"] / r["reqs"] * 100

    scenarios = ["Part A\n(broad chaos, ~10%)", "Part B\n(single replica drop)"]
    retry_on = [pct(part_a_on), pct(part_b_on)]
    retry_off = [pct(part_a_off), pct(part_b_off)]

    fig, ax = plt.subplots(figsize=(8, 5))
    x = list(range(len(scenarios)))
    w = 0.35
    ax.bar([i - w / 2 for i in x], retry_on, w, label="retries on", color="#d9534f")
    ax.bar([i + w / 2 for i in x], retry_off, w, label="retries off", color="#5cb85c")

    # Value labels
    for i, (on, off) in enumerate(zip(retry_on, retry_off)):
        ax.text(i - w / 2, on + 1, f"{on:.2f}%", ha="center", fontsize=10)
        ax.text(i + w / 2, off + 1, f"{off:.2f}%", ha="center", fontsize=10)

    ax.set_ylabel("Client-observed failure rate (%)")
    ax.set_xticks(x)
    ax.set_xticklabels(scenarios)
    ax.set_title(
        "Retry efficacy flips with failure breadth\n"
        "Broad chaos: retry amplifies cascading DOWN-marks. Narrow failure: retry absorbs the drop."
    )
    ax.legend(loc="upper right")
    ax.set_ylim(0, max(retry_on) * 1.15)
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)

    # Part B "N× fewer failures" annotation uses raw failure-count ratio
    # (784 / 18 ≈ 44×) to match the narrative in §4.3.
    part_b_fail_ratio = part_b_off["fails"] / part_b_on["fails"]
    ax.annotate(
        f"{part_b_fail_ratio:.0f}× fewer failures",
        xy=(1 + w / 2, retry_off[1]),
        xytext=(1.4, retry_off[1] + 20),
        fontsize=10,
        color="#2e7d32",
        arrowprops=dict(arrowstyle="->", color="#2e7d32"),
    )
    ax.annotate(
        f"{retry_on[0] / retry_off[0]:.1f}× more failures",
        xy=(0 - w / 2, retry_on[0]),
        xytext=(-0.3, retry_on[0] + 5),
        fontsize=10,
        color="#c62828",
        arrowprops=dict(arrowstyle="->", color="#c62828"),
    )

    fig.tight_layout()
    fig.savefig(FIG_DIR / "retry_flip.png", dpi=150)
    plt.close(fig)
    print("  ok: retry_flip.png")


# -----------------------------------------------------------------------------
# Figure 3: /api/data scaling curve (RPS vs lb_count at u=500 and u=2000)
# -----------------------------------------------------------------------------
def fig_scaling_data() -> None:
    lb_counts = [1, 2, 4, 8]
    u500_rps, u2000_rps = [], []
    for n in lb_counts:
        u500 = _aggregated_row(RESULTS / "exp3" / f"lb{n}_u500" / "stats.csv")
        u2000 = _aggregated_row(RESULTS / "exp3" / f"lb{n}_u2000" / "stats.csv")
        u500_rps.append(u500["rps"])
        u2000_rps.append(u2000["rps"])

    # Ideal linear scaling reference (lb=1 as baseline)
    ideal_u500 = [u500_rps[0] * n for n in lb_counts]
    ideal_u2000 = [u2000_rps[0] * n for n in lb_counts]

    fig, ax = plt.subplots(figsize=(8, 5))
    ax.plot(lb_counts, u500_rps, "o-", label="u=500 (measured)", linewidth=2, markersize=8)
    ax.plot(
        lb_counts,
        ideal_u500,
        "o--",
        label="u=500 (linear ideal)",
        linewidth=1,
        alpha=0.4,
        color="C0",
    )
    ax.plot(
        lb_counts,
        u2000_rps,
        "s-",
        label="u=2000 (measured)",
        linewidth=2,
        markersize=8,
        color="C1",
    )
    ax.plot(
        lb_counts,
        ideal_u2000,
        "s--",
        label="u=2000 (linear ideal)",
        linewidth=1,
        alpha=0.4,
        color="C1",
    )

    for n, rps in zip(lb_counts, u500_rps):
        ax.text(n, rps - 350, f"{rps:.0f}", ha="center", fontsize=9, color="C0")
    for n, rps in zip(lb_counts, u2000_rps):
        ax.text(n, rps + 200, f"{rps:.0f}", ha="center", fontsize=9, color="C1")

    ax.set_xscale("log", base=2)
    ax.set_xticks(lb_counts)
    ax.set_xticklabels([str(n) for n in lb_counts])
    ax.set_xlabel("LB instances (log scale)")
    ax.set_ylabel("Aggregate RPS")
    ax.set_title(
        "LB horizontal scaling on /api/data\n"
        "Near-linear 1→4, sublinear at 8. Measured vs linear ideal."
    )
    ax.grid(True, alpha=0.3)
    ax.legend(loc="upper left", fontsize=9)
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)
    fig.tight_layout()
    fig.savefig(FIG_DIR / "scaling_data.png", dpi=150)
    plt.close(fig)
    print("  ok: scaling_data.png")


def main() -> None:
    print(f"Generating figures in {FIG_DIR.relative_to(ROOT)}/")
    fig_hetero_distribution()
    fig_retry_flip()
    fig_scaling_data()
    print("done.")


if __name__ == "__main__":
    main()
