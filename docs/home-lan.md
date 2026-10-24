# Home LAN Quickstart

Run `herd daemon` with zero flags on each machine — desktop, laptop, Pis —
and they find each other over mDNS and form one mesh. No `--join` addresses,
no walking up and down the stairs.

```bash
# On every machine (hostname becomes the node name):
herd daemon

# Check from any of them:
herd roster
```

Convergence takes seconds on a working LAN. If a node doesn't appear:

1. Same LAN and multicast working? (Guest WiFi and some mesh extenders
   block mDNS.)
2. Firewall allowing UDP 5353 (mDNS) and your gossip port (default 7946)?
3. Multihomed host (Docker, VPNs)? Pin the announced address:
   `herd daemon --advertise-addr 192.168.1.50`.
4. Disable discovery and join manually:
   `herd daemon --mdns=false`, then `herd join <addr>` as before.

A node booted before any peer keeps browsing and joins when one appears;
a slept/woken laptop re-heals without a restart.

## Trust model (v1)

The LAN mesh is **open**: any herd node on your LAN can join, same as
before (previously it just needed to guess an address). Discovery changes
reachability, not trust.

- Keep it that way on trusted home networks: nothing to configure.
- A shared **mesh key** (cryptographic gate: no key, no join) is a planned
  follow-up. Discovery already reserves a key-fingerprint field, so adding
  it later changes no announce/browse behavior.
- Hostname clashes (two boxes named `node-1`) log a warning and never flap
  the roster; rename one box to fix it.
