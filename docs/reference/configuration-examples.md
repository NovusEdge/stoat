# Configuration examples

Stoat uses TOML files for VM settings, guest definitions, projects, and
recipes. The annotated samples in the repository describe their fields:

| File | Purpose | Reference |
|---|---|---|
| [vm.toml](https://github.com/NovusEdge/stoat/blob/main/docs/reference/samples/vm.toml) | Settings and recorded state for one VM | [The data root](../concepts/data-root.md) |
| [guest.toml](https://github.com/NovusEdge/stoat/blob/main/docs/reference/samples/guest.toml) | Guest image and provisioning defaults | [Guest definitions](guest.md) |
| [stoat.toml](https://github.com/NovusEdge/stoat/blob/main/docs/reference/samples/stoat.toml) | VMs declared in a project | [The project file](project-file.md) |
| [recipe.toml](https://github.com/NovusEdge/stoat/blob/main/internal/recipes/samples/recipe.toml) | Recipe metadata and parameters | [Writing your own recipe](../recipes/writing-your-own.md) |

These files document the available fields. For a first working environment,
follow [Your first VM](../getting-started/first-vm.md) or the
[Project workflow](../guides/project-workflow.md).
