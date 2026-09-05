package mcpsrv

import "github.com/novusedge/stoat/internal/cli/wire"

// Contract is the JSON contract this server speaks. It is the same constant
// stoat --json version reports, so the runtime check the Python server did
// against a separate process is now a compile-time fact.
const Contract = wire.ContractVersion
