# eprom

A terminal dashboard for a Prometheus metrics endpoint. eprom scrapes the URL
periodically and draws a graph for every metric family, titled with the
metric's `HELP` text. History is kept in memory only and starts empty.

## Install

Download a binary for Linux, macOS or Windows from the
[releases page](https://github.com/espebra/eprom/releases), or build from
source:

```sh
go install github.com/espebra/eprom@latest
```

## Dependencies

All dependencies are vendored in `vendor/`, so builds need no network
access. After changing dependencies, run `go mod tidy && go mod vendor`.
Dependabot opens weekly pull requests for Go modules and GitHub Actions.

## Releasing

Push a tag of the form `vX.Y.Z`. The release workflow runs the tests, builds
binaries for Linux, macOS and Windows (amd64 and arm64) and publishes them as a GitHub
release:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Usage

```sh
eprom [flags] <metrics-url>

eprom http://localhost:9100/metrics
eprom -interval 5s -height 10 localhost:9090/metrics
```

| Flag          | Default | Description                                |
|---------------|---------|--------------------------------------------|
| `-interval`   | `2s`    | Scrape interval (also the request timeout) |
| `-history`    | `1000`  | Samples kept per series                    |
| `-height`     | `8`     | Chart height in rows                       |
| `-series`     | `6`     | Maximum series drawn per metric            |
| `-all-series` | `25`    | Maximum series drawn per metric after `a`  |

### Keys

| Key               | Action                         |
|-------------------|--------------------------------|
| `j` / `k`, arrows | Scroll one metric              |
| `space` / `b`     | Page down / up                 |
| `g` / `G`         | Top / bottom                   |
| `/`               | Filter by metric name or HELP  |
| `a`               | Toggle showing more series     |
| `esc`             | Clear filter                   |
| `q`               | Quit                           |

## How metric types are plotted

| Type      | Plotted as                                                        |
|-----------|-------------------------------------------------------------------|
| gauge     | Raw value                                                         |
| untyped   | Raw value                                                         |
| counter   | Per-second rate between scrapes; decreases are treated as resets |
| summary   | Reported quantiles, or average observation if none are exposed    |
| histogram | p50/p90/p99 estimated from bucket deltas over each interval       |

Counters, histograms and quantile-less summaries need two scrapes before the
first point appears. A histogram with no new observations during an interval
leaves a gap. Each label set is a separate series. Failed scrapes also show up
as gaps.

### Units

The text format has no unit metadata, so units are taken from the metric name,
following the Prometheus naming conventions:

| Name ends in                                  | Shown as                                   |
|-----------------------------------------------|--------------------------------------------|
| `_bytes`, `_bytes_total`                      | B, KB, MB, GB, TB, PB (powers of 1000)      |
| `_seconds` (gauge, histogram, summary)        | Duration: ns, µs, ms, s, min, h, d          |
| `_seconds_total` (counter)                    | Plain ratio, e.g. busy CPU cores            |
| `_time_seconds`, `_timestamp_seconds`, `_timestamp` | Age: "3.1d ago", or "in 2d" if in the future |

A zero timestamp is treated as "never" and leaves a gap.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
