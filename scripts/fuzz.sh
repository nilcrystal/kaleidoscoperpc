#!/usr/bin/env bash
set -euo pipefail

FUZZTIME="${FUZZTIME:-10s}"
for target in \
  FuzzBase85RoundTrip \
  FuzzBase85StrictDecode \
  FuzzServiceParser \
  FuzzDecodeRequestNoPanic \
  FuzzBinaryMessageRoundTrip \
  FuzzTextValueValidation
do
  go test -run='^$' -fuzz="^${target}$" -fuzztime="${FUZZTIME}"
done
