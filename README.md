# recasaos-wake-on-lan

An optional module of [ReCasaOS](https://github.com/ReCasaOS) that wakes a computer of your
network from the box, with a Wake-on-LAN magic packet.

It is also meant as an example: a small, complete module written outside the ReCasaOS
organisation, that another author can copy to start a module of their own. The second half of
this page is for them.

## For the person who runs the box

### What it does

You keep a list of the computers of your network (a name and a MAC address each). Each one has a
**Wake** button. The box sends the magic packet to the broadcast address of each of its IPv4
networks, on UDP port 9, and the computer starts, if Wake-on-LAN is turned on in its firmware and
in its network settings.

- The module lets any signed-in account wake a saved device and only the administrator change the list; on a ReCasaOS box the gateway gives other accounts no write access, so in practice the administrator does both. The dashboard shows the card to administrators only.
- A computer can be woken only once it is in the list: the module never sends a packet for a MAC
  address that is not saved, and never to an address given from outside. The box sends nothing
  but magic packets, to its own networks.

### Install

On a box running ReCasaOS v0.5.199 or later: in the dashboard, **Modules**, then *Wake-on-LAN*,
**Install**; or on the box, `sudo casaos module install wake-on-lan`.

It is a module of the `community` tier: written by inkly, not by ReCasaOS. It needs no package and
takes about 10 MB.

### Where its page is

Open `http://<your box>/v1/wake_on_lan/`, at the same address as your dashboard (if you open the
dashboard at `http://192.168.1.10`, the page is `http://192.168.1.10/v1/wake_on_lan/`). The page
uses your sign-in to the dashboard: if it asks you to sign in, open the dashboard, sign in, and
come back.

### What it keeps

The list is in `/var/lib/casaos-wake-on-lan/devices.json`, readable by root only. Its settings
are in `/etc/casaos/wake-on-lan.conf`, written from `wake-on-lan.conf.sample` the first time the
service starts.

The service logs to `/var/log/casaos/wake-on-lan.log`.

`sudo casaos module remove wake-on-lan` takes the module away. The list, the settings and the log
stay; `sudo rm -r /var/lib/casaos-wake-on-lan /etc/casaos/wake-on-lan.conf /var/log/casaos/wake-on-lan.log`
removes them.

### The API

The page and the card call five routes under `/v1/wake_on_lan`, each with the dashboard's token in the
`Authorization` header. The API is described in `openapi.yaml`, at
`http://<your box>/doc/v1/wake_on_lan/openapi.yaml` (and in `api/wake_on_lan/` of this repository).

| Route | Who | Does |
|---|---|---|
| `GET /devices` | any signed-in account | the list, and `can_edit`: whether the caller may change it |
| `PUT /devices` | the administrator | replaces the list, whole: `{"devices": [{"name": "...", "mac": "..."}]}` |
| `POST /wake` | any signed-in account | `{"mac": "..."}`: sends the packet of a saved computer |
| `GET /card` | any signed-in account | the card of the home screen (see below) |
| `GET /status` | any signed-in account | the module's version |

A list holds 64 computers at most; a name has 1 to 64 printable characters; a MAC address is six
bytes in hexadecimal separated by `:` or `-`; a body has 64 KiB at most. A computer is woken at
most once a second (a faster call answers 429). A save replaces the whole list: if two
administrators edit it at once, the last save wins. An account that was removed is refused on
every route.

## For an author who starts from this repository

A module is a service behind the box's gateway, installed by `casaos module install` from an
archive that a catalogue of ReCasaOS names. The contract is
[docs/modules.md](https://github.com/ReCasaOS/CasaOS-Install/blob/main/docs/modules.md) of
ReCasaOS/CasaOS-Install; read its sections *The manifest*, *What an archive may write* and
*casaos module* before you change the layout below.

### The files, and why each is there

| File | Why |
|---|---|
| `module.json` | The manifest: the module's name, title, description, unit, gateway route, packages, the oldest ReCasaOS it works with and the systems it supports. The box reads it before it downloads anything, and refuses an install on a system it does not list. Every value has a fixed pattern (see the contract). |
| `main.go` | Starts the service: reads the settings, listens on `127.0.0.1` on a free port, writes that address to `/var/run/casaos/wake-on-lan.url`, tells systemd it is ready, and registers its routes with the gateway. The gateway may not be up yet at boot: the registration is tried again, after 1, 2, 4, 8 and then every 10 seconds, until it works. |
| `route/` | The routes. Every API route checks the dashboard's token (`echojwt` with the public key of the box's user-service, or a request of one of the box's own services from loopback with the box's internal secret); an account that user-service says was removed is refused; `PUT /devices` also checks that the account is the administrator. Bodies are limited to 64 KiB, and a computer is woken once a second at most. The page itself needs no token: it holds no data. |
| `service/` | What the module does: the magic packet and the broadcast addresses (`packet.go`), the list of computers and its file (`devices.go`), written whole under another name and renamed, so it is never half written. |
| `web/index.html` | The page: one HTML file, plain JavaScript, nothing loaded from anywhere else. It reads the token the dashboard keeps in the browser (`localStorage`, `access_token`) and sends it the way the dashboard does, bare in `Authorization`. Every value from the API goes in as text (`textContent`), never as HTML. |
| `api/` | The description of the API, `openapi.yaml`, served at `/doc/v1/wake_on_lan/openapi.yaml`. |
| `pkg/config/`, `common/` | The settings file and the version (`common.Version`, printed by `casaos-wake-on-lan -v`). |
| `build/sysroot/` | What the archive puts on the box, at the same paths: the systemd unit and the sample of the settings. The binary is added there by the build. A box writes nothing else from an archive: a program in `/usr/bin`, the one unit the manifest names, and `*.sample` files in `/etc/casaos`. |
| `.goreleaser.yaml` | Builds the binary for amd64 and arm64 and the two archives, `linux-<arch>-casaos-wake-on-lan-v<version>.tar.gz`, each with `module.json`, `LICENSE` and `NOTICE` at its root, and `checksums.txt`. |
| `LICENSE`, `NOTICE` | Apache-2.0, and where the code comes from. |
| `.gitattributes` | Keeps the files as they are on every system: the catalogue compares `module.json` of the archives with the one of the repository byte for byte. |

To make a module of your own: rename the module (`name`, `component` = your repository's name,
`unit`, `gateway_route`, the files under `build/sysroot/`, `project_name`, the paths in `main.go`
and `pkg/config`), and replace `service/`, `route/`, `web/` and `api/` with what your module does.
Keep the token check of `route/` as it is. The archives' names need not be the repository's,
but must be `linux-<arch>-casaos-<stem>-v<version>.tar.gz`: this repository is
`recasaos-wake-on-lan` and its archives are `…-casaos-wake-on-lan-…`.

### The card

The dashboard draws a card on its home screen from `GET /v1/wake_on_lan/card`, which it reads
every `refresh` seconds. The module answers with the card itself: a JSON object, with no envelope
around it. Card format v1 is read strictly: a field it does not know, a wrong type or a bound
passed (a title and a label of 40 characters, 8 rows, a button label of 24, a body of 1 KiB, a
`refresh` of 30 to 3600) and the whole card is refused. An action is a `POST` of the module's own
route, `path` relative to `/v1/wake_on_lan`; the card never names another address.

Here it shows the number of devices, and a row for each, by name: the name, the MAC address and a
**Wake** button (`POST /wake`). With more than 8 devices it shows 7 and a last row, "…and N more",
with no button; with none, one row that says to add a device from the page. The link opens the page.

```json
{
  "schema": 1,
  "title": "Wake-on-LAN",
  "icon": "network-outline",
  "status": {"level": "info", "text": "2 devices"},
  "rows": [
    {"label": "Desk PC", "value": "aa:bb:cc:dd:ee:01",
     "action": {"label": "Wake", "method": "POST", "path": "/wake", "body": {"mac": "aa:bb:cc:dd:ee:01"}}},
    {"label": "NAS", "value": "aa:bb:cc:dd:ee:02",
     "action": {"label": "Wake", "method": "POST", "path": "/wake", "body": {"mac": "aa:bb:cc:dd:ee:02"}}}
  ],
  "link": {"label": "Open", "path": "/"},
  "refresh": 60
}
```

The card is built in `route/card.go`, and `route/card_test.go` checks it against the bounds of the
format for 0, 1, 8 and 20 devices.

### Build and test

```sh
go vet ./...
go test -race ./...
```

The archives are built with GoReleaser v2.17.0 (it builds with Go 1.26.8; later versions need a
newer Go): `go install github.com/goreleaser/goreleaser/v2@v2.17.0`. The catalogue matches the
names of the archives to the tag, so build them from a tag, without publishing anything:

```sh
git tag v0.2.0                                   # a local tag; do not push it to try a build
goreleaser release --clean --skip=publish,announce
# dist/linux-amd64-casaos-wake-on-lan-v0.2.0.tar.gz, dist/linux-arm64-..., dist/checksums.txt
git tag -d v0.2.0
```

GoReleaser needs a git remote to read (any URL: nothing is pushed). The build is reproducible:
`-trimpath`, and the commit's date for the binary's date and every file's time.

The tests need no network: the sending of the packet and the network interfaces are functions
that the tests replace.

### Publish a version

1. Set the version in `common/constants.go` (it is what `-v` prints) and commit it.
2. Tag the commit `v<major>.<minor>.<patch>` (no leading zero) and push the tag.
3. `.github/workflows/release.yml` runs on the tag, on a runner hosted by GitHub: it runs the
   tests, builds with GoReleaser v2.17.0, publishes the release of the tag with the two archives
   and `checksums.txt`, and attests the provenance of both archives (`actions/attest`).

A catalogue of ReCasaOS takes an archive of another owner than ReCasaOS only with a GitHub
provenance attestation bound to the repository, the tag and the commit the tag names. Anyone can
check it, for each of the two archives, with the commit of the tag (`git rev-parse v0.2.0^{commit}`):

```sh
gh attestation verify linux-amd64-casaos-wake-on-lan-v0.2.0.tar.gz \
  --repo inkly/recasaos-wake-on-lan --source-digest <commit of the tag> \
  --source-ref refs/tags/v0.2.0 --deny-self-hosted-runners
```

A published version never changes: a fix is a new tag.

### Propose the module to ReCasaOS

The modules a box can install are listed in `modules.env` of
[ReCasaOS/modules](https://github.com/ReCasaOS/modules). Fork it, add your module, and open a
pull request from your fork. A module of another owner has seven lines; `<NAME>` is the module's
name in upper case, with `_` for `-`:

```sh
MODULE_WAKE_ON_LAN_TAG=v0.2.0
MODULE_WAKE_ON_LAN_COMMIT=<the 40 hexadecimal digits of the commit the tag names>
MODULE_WAKE_ON_LAN_COMPONENT=recasaos-wake-on-lan
MODULE_WAKE_ON_LAN_TIER=community
MODULE_WAKE_ON_LAN_OWNER=inkly
MODULE_WAKE_ON_LAN_SHA256_AMD64=<the SHA-256 of linux-amd64-casaos-wake-on-lan-v0.2.0.tar.gz>
MODULE_WAKE_ON_LAN_SHA256_ARM64=<the SHA-256 of linux-arm64-casaos-wake-on-lan-v0.2.0.tar.gz>
```

The two SHA-256 are the ones in the `checksums.txt` of your release (`sha256sum` of the archives
you downloaded gives the same). The pull request is checked: the tag names the commit,
`module.json` of the repository at that commit is the one of both archives, byte for byte, the
archives have the pinned SHA-256 and their attestation, and they hold nothing a box would not
unpack. Once it is merged, ReCasaOS keeps a copy of your archives and the boxes download that
copy, so a version that was reviewed stays the one installed.

You can run the same judgement before you open the pull request, with
`scripts/build-modules-catalogue.py` of ReCasaOS/CasaOS-Install (Python 3.12): put
`checksums.txt` and the two archives in `<stage>/<name>/`, write the seven lines above to a
`modules.env`, and run

```sh
python3 build-modules-catalogue.py build --git-base https://github.com/ReCasaOS --out catalogue.json \
  --schema 3 --serial 1 --generated 2026-01-01T00:00:00Z --withdrawn /dev/null \
  modules.env <stage>
```

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
