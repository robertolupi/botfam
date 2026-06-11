# botfam-irc: ergo with the botfam test config baked in.
# ircd.yaml is derived from ergo v2.18.0's default.yaml (see README).
# The upstream entrypoint (run.sh) uses /ircd/ircd.yaml when it exists,
# so baking it in skips upstream's config generation.
FROM ghcr.io/ergochat/ergo:v2.18.0
COPY ircd.yaml /ircd/ircd.yaml
RUN mkdir -p /ircd/data
