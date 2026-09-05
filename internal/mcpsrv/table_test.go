package mcpsrv

// toolTable is the source of truth for the tool set: every tool, its
// annotation class, and the agent_access level it needs. docs/design/
// mcp-server.md links here rather than repeating it.
var toolTable = []toolSpec{
	// Read-only (Task 5).
	{"list_vms", classRead, LevelNone},
	{"vm_status", classRead, LevelNone},
	{"list_images", classRead, LevelNone},
	{"list_recipes", classRead, LevelNone},
	{"check_recipes", classRead, LevelNone},
	{"logs", classRead, LevelNone},
	{"doctor", classRead, LevelNone},
	{"plan_recipes", classRead, LevelNone},
	{"list_guests", classRead, LevelNone},
	{"guest_info", classRead, LevelNone},
	{"recipe_schema", classRead, LevelNone},
	{"search_recipes", classRead, LevelNone},

	// Mutating, host side (Task 7).
	{"create", classMutate, LevelNone},
	{"start", classMutate, LevelNone},
	{"stop", classMutate, LevelNone},
	{"update", classMutate, LevelNone},
	{"clone", classMutate, LevelNone},
	{"snapshot", classMutate, LevelNone},
	{"forward", classMutate, LevelNone},
	{"wait", classMutate, LevelNone},

	// Destructive, host side (Task 7).
	{"destroy", classDestructive, LevelNone},
	{"prune", classDestructive, LevelNone},
	{"restore", classDestructive, LevelNone},

	// Recipe index (Task 15).
	{"add_recipe", classMutate, LevelNone},
	{"update_recipe", classMutate, LevelNone},
	{"remove_recipe", classMutate, LevelNone},

	// Guest, observe (Task 10).
	{"read_file", classRead, LevelObserve},
	{"list_dir", classRead, LevelObserve},
	{"stat", classRead, LevelObserve},
	{"ps", classRead, LevelObserve},
	{"svc_status", classRead, LevelObserve},
	{"tail_log", classRead, LevelObserve},

	// Guest, manage (Tasks 7 and 11).
	{"apply_recipes", classExec, LevelManage},
	{"write_file", classExec, LevelManage},
	{"copy_to", classExec, LevelManage},
	{"copy_from", classExec, LevelManage},
	{"pkg_install", classExec, LevelManage},
	{"svc", classExec, LevelManage},
	{"useradd", classExec, LevelManage},

	// Guest, exec (Task 12).
	{"exec", classExec, LevelExec},
	{"exec_bg", classExec, LevelExec},
	{"job_status", classRead, LevelExec},
	{"job_output", classRead, LevelExec},
	{"job_kill", classExec, LevelExec},
	{"list_jobs", classRead, LevelExec},
}

// forbiddenSurfaces are absent rather than gated. A parameter that exists
// is eventually reachable.
var forbiddenSurfaces = []string{"recipe_new", "ssh_command", "global_logs", "pull"}

// forbiddenInputFields may not appear as a property on any tool's input
// schema, at any depth.
var forbiddenInputFields = []string{"share", "base", "iso", "console_password", "image_path", "byo"}

// pending names tools a later task registers. Every entry is removed by the
// task that adds the tool; Task 18 asserts the list is empty.
//
// Tasks 7, 10, 11 and 12 own the rest of this chunk's tools and are gone
// from this map already: their tests assert real registration, which does
// not exist yet, so TestEveryTableToolIsRegistered fails for them until
// their implementer lands.
var pending = map[string]string{
	"search_recipes": "Task 14", "add_recipe": "Task 15", "update_recipe": "Task 15",
	"remove_recipe": "Task 15",
}
