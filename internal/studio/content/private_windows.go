package content

import "os"

// Windows deployments must protect the data directory with a private NTFS ACL.
// FileMode does not describe Windows access control lists.
func privateStateDirectory(_ os.FileInfo) bool { return true }
