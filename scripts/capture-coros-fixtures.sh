#!/usr/bin/env bash
set -euo pipefail

for bin in curl jq; do
  command -v "$bin" >/dev/null || { echo "missing dependency: $bin" >&2; exit 1; }
done

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
out="$repo_root/testdata/coros"
raw="$(mktemp -d)"
trap 'rm -rf "$raw"' EXIT
mkdir -p "$out"

login_host="https://teamapi.coros.com"
lookback_days="${LOOKBACK_DAYS:-90}"
page_size="${PAGE_SIZE:-2}"

md5hex() {
  if command -v md5sum >/dev/null; then md5sum | cut -d' ' -f1; else md5 -q; fi
}

region_host() {
  case "$1" in
    1) echo "https://teamapi.coros.com" ;;
    2) echo "https://teamcnapi.coros.com" ;;
    3) echo "https://teameuapi.coros.com" ;;
    *) echo "unknown regionId $1" >&2; exit 1 ;;
  esac
}

other_host() {
  case "$1" in
    3) echo "https://teamapi.coros.com" ;;
    *) echo "https://teameuapi.coros.com" ;;
  esac
}

status_log="$raw/statuses"
: >"$status_log"

call() {
  local name="$1" method="$2" url="$3" token_header="${4:-}" body="${5:-}"
  local args=(-sS -X "$method" -o "$raw/$name.json" -D "$raw/$name.headers" -w '%{http_code}')
  if [[ -n "$token_header" ]]; then args+=(-H "@$token_header"); fi
  if [[ -n "$body" ]]; then args+=(-H 'Content-Type: application/json' --data-binary "@$body"); fi
  local code
  code="$(curl "${args[@]}" "$url")"
  local ctype
  ctype="$(grep -i '^content-type:' "$raw/$name.headers" | head -1 | cut -d' ' -f2- | tr -d '\r' || true)"
  printf '%s\t%s\t%s\n' "$name" "$code" "$ctype" >>"$status_log"
  echo "  $name -> HTTP $code, result $(jq -r '.result // "n/a"' "$raw/$name.json" 2>/dev/null || echo 'non-json')"
}

token_file() {
  local file="$raw/$1.header"
  printf 'accessToken: %s\n' "$2" >"$file"
  chmod 600 "$file"
  echo "$file"
}

login_body() {
  local file="$raw/$1.body"
  COROS_PWD_MD5="$2" jq -n --arg account "$COROS_EMAIL" \
    '{account: $account, accountType: 2, pwd: $ENV.COROS_PWD_MD5}' >"$file"
  chmod 600 "$file"
  echo "$file"
}

if [[ -z "${COROS_EMAIL:-}" ]]; then read -r -p "COROS email: " COROS_EMAIL; fi
if [[ -z "${COROS_PASSWORD:-}" ]]; then read -r -s -p "COROS password: " COROS_PASSWORD; echo; fi
pwd_md5="$(printf '%s' "$COROS_PASSWORD" | md5hex)"
unset COROS_PASSWORD

echo "1/7 login with wrong password"
call login-bad-credentials POST "$login_host/account/login" "" "$(login_body bad "$(printf '%s' "not-the-password-$RANDOM" | md5hex)")"

echo "2/7 login"
call login POST "$login_host/account/login" "" "$(login_body good "$pwd_md5")"
token1="$(jq -r '.data.accessToken' "$raw/login.json")"
region_id="$(jq -r '.data.regionId' "$raw/login.json")"
user_id="$(jq -r '.data.userId // empty' "$raw/login.json")"
nickname="$(jq -r '.data.nickname // empty' "$raw/login.json")"
[[ "$token1" != "null" && -n "$token1" ]] || { echo "login failed, see result above" >&2; exit 1; }
home="$(region_host "$region_id")"
wrong="$(other_host "$region_id")"
h1="$(token_file t1 "$token1")"
echo "  regionId $region_id -> $home"

end_day="$(date -u +%Y%m%d)"
start_day="$(date -u -v-"${lookback_days}"d +%Y%m%d 2>/dev/null || date -u -d "-${lookback_days} days" +%Y%m%d)"
query="startDay=$start_day&endDay=$end_day&modeList=0"

echo "3/7 region verification and wrong region"
call query-verify GET "$home/activity/query?size=1&pageNumber=1&$query" "$h1"
call query-wrong-region GET "$wrong/activity/query?size=1&pageNumber=1&$query" "$h1"

echo "4/7 activity list, pages 1 and 2 (size $page_size, last $lookback_days days)"
call query-activities-page-1 GET "$home/activity/query?size=$page_size&pageNumber=1&$query" "$h1"
total_page="$(jq -r '.data.totalPage' "$raw/query-activities-page-1.json")"
if [[ "$total_page" -lt 2 ]]; then
  echo "only $total_page page(s); rerun with a larger LOOKBACK_DAYS" >&2
  exit 1
fi
call query-activities-page-2 GET "$home/activity/query?size=$page_size&pageNumber=2&$query" "$h1"
call query-activities-past-last-page GET "$home/activity/query?size=$page_size&pageNumber=$((total_page + 1))&$query" "$h1"

echo "5/7 download"
read -r label_id sport_type < <(jq -r '.data.dataList[0] | "\(.labelId) \(.sportType)"' "$raw/query-activities-page-1.json")
call download-fit POST "$home/activity/detail/download?labelId=$label_id&sportType=$sport_type&fileType=4" "$h1"
file_url="$(jq -r '.data.fileUrl' "$raw/download-fit.json")"
file_code="$(curl -sS -o /dev/null -D "$raw/file.headers" -w '%{http_code}' "$file_url")"
file_ctype="$(grep -i '^content-type:' "$raw/file.headers" | head -1 | cut -d' ' -f2- | tr -d '\r' || true)"
printf '%s\t%s\t%s\n' "file-download" "$file_code" "$file_ctype" >>"$status_log"
echo "  file GET -> HTTP $file_code ($file_ctype), body discarded"

echo "6/7 download for an activity without a file"
if [[ -n "${MANUAL_LABEL_ID:-}" && -n "${MANUAL_SPORT_TYPE:-}" ]]; then
  call download-no-file POST "$home/activity/detail/download?labelId=$MANUAL_LABEL_ID&sportType=$MANUAL_SPORT_TYPE&fileType=4" "$h1"
else
  echo "  skipped: set MANUAL_LABEL_ID and MANUAL_SPORT_TYPE to capture it"
fi

echo "7/7 second login revokes the first token"
call login-again POST "$login_host/account/login" "" "$(login_body good2 "$pwd_md5")"
token2="$(jq -r '.data.accessToken' "$raw/login-again.json")"
call query-token-revoked GET "$home/activity/query?size=1&pageNumber=1&$query" "$h1"
h2="$(token_file t2 "$token2")"
call query-after-relogin GET "$home/activity/query?size=1&pageNumber=1&$query" "$h2"

label_map="$raw/label-map.json"
jq -s '[.[] | .data.dataList[]?.labelId] + [env.MANUAL_LABEL_ID // empty]
       | reduce .[] as $id ({}; if has($id) then . else . + {($id): ("90000000000000" + ("0000" + (length + 2 | tostring))[-4:])} end)' \
  "$raw/query-activities-page-1.json" "$raw/query-activities-page-2.json" >"$label_map"

anchor=1741600000
latest="$(jq -s '[.[] | .data.dataList[]?.startTime] | max' "$raw/query-activities-page-1.json" "$raw/query-activities-page-2.json")"
shift_by=$((anchor - latest))

redact='
def lid: . as $id | ($map[0][$id] // $id);
def day: strftime("%Y%m%d") | tonumber;
walk(
  if type == "object" then
    (if has("accessToken") then .accessToken = "test-access-token" else . end)
    | (if has("userId") then .userId = "900000000000000001" else . end)
    | (if has("email") then .email = "runner@example.com" else . end)
    | (if has("nickname") then .nickname = "Runner" else . end)
    | (if has("countryCode") then .countryCode = "US" else . end)
    | (if has("tlogId") then .tlogId = "90000000000000022" else . end)
    | (if has("deviceId") then .deviceId = "A00000" else . end)
    | (if has("imageUrl") then .imageUrl = "" else . end)
    | (if has("headPic") then .headPic = "" else . end)
    | (if has("fileUrl") then .fileUrl = "http://coros-files.test/\($label).fit" else . end)
    | (if has("labelId") then .labelId = (.labelId | lid) else . end)
    | (if has("startTime") and (.startTime | type) == "number" then .startTime += $shift else . end)
    | (if has("endTime") and (.endTime | type) == "number" then .endTime += $shift else . end)
    | (if has("startTime") and has("date") then .date = (.startTime | day) else . end)
  else . end
)
| if (.data | type) == "object" and (.data | has("accessToken")) then
    .data |= with_entries(select(.key | IN("accessToken", "userId", "nickname", "email", "countryCode", "regionId", "unit", "temperatureUnit", "twoFactorRequired")))
  else . end
| if (.data | type) == "object" and (.data | has("dataList")) then
    .data.dataList |= map(
      with_entries(
        if (.key | IN("labelId", "sportType", "name", "date", "startTime", "endTime", "mode", "subMode", "unitType", "imageUrlType")) then .
        elif (.value | type) == "number" then .value = 0
        elif (.value | type) == "string" then .value = ""
        else .value = null end
      )
      | .name = "Activity \(.labelId[-4:] | tonumber - 1)"
      | .device = "COROS Watch"
      | .deviceId = "A00000"
    )
  else . end
'

mapped_label="$(jq -r --arg id "$label_id" '.[$id]' "$label_map")"
for name in login-bad-credentials login query-verify query-wrong-region query-activities-page-1 query-activities-page-2 query-activities-past-last-page download-fit download-no-file query-token-revoked; do
  [[ -f "$raw/$name.json" ]] || continue
  jq --slurpfile map "$label_map" --argjson shift "$shift_by" --arg label "$mapped_label" "$redact" "$raw/$name.json" >"$out/$name.json"
done

jq -R -s 'split("\n") | map(select(length > 0) | split("\t")) | map({key: .[0], value: {status: (.[1] | tonumber), contentType: .[2]}}) | from_entries' \
  "$status_log" >"$out/http.json"

leaks=("$COROS_EMAIL" "$token1" "$token2" "$file_url")
[[ -n "$user_id" ]] && leaks+=("$user_id")
[[ -n "$nickname" && ${#nickname} -ge 3 ]] && leaks+=("$nickname")
while read -r id; do leaks+=("$id"); done < <(jq -r 'keys[]' "$label_map")
failed=0
for secret in "${leaks[@]}"; do
  if grep -rqF -- "$secret" "$out"; then
    echo "LEAK: an identifier is still present in $out" >&2
    failed=1
  fi
done
if [[ $failed -ne 0 ]]; then
  rm -f "$out"/*.json
  echo "redaction failed; fixtures removed" >&2
  exit 1
fi

echo
echo "Fixtures written to $out. Your COROS Training Hub session was logged out by these logins."
echo "Review every file before committing (activity names, metrics, any field you do not recognise)."
