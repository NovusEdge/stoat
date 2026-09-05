{
  description = "stoat: a terminal UI for local QEMU VMs (Alpine-first, no libvirt, no daemon)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      # Matches the systems built by .github/workflows/release.yml
      # (linux/amd64, linux/arm64).
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];

      eachSystem =
        f:
        nixpkgs.lib.genAttrs systems (
          system: f nixpkgs.legacyPackages.${system}
        );
    in
    {
      packages = eachSystem (
        pkgs:
        let
          # `git describe` isn't available in the nix build sandbox, so pin a
          # version string here; bump on release the same way release.yml's
          # ${GITHUB_REF_NAME} does for the tarball builds.
          version = "0.2.0";
        in
        {
          default = pkgs.buildGoModule {
            pname = "stoat";
            inherit version;

            src = self;

            # Recompute when go.mod or go.sum changes.
            vendorHash = "sha256-Wg91Gxj25SbUttCot+YzmWw7cgZ+O2r82AHx6FSYXIo=";

            subPackages = [ "cmd/stoat" ];

            ldflags = [
              "-s"
              "-w"
              "-X main.version=v${version}"
            ];

            meta = {
              description = "A terminal UI for local QEMU VMs. Alpine-first, no libvirt, no daemon.";
              homepage = "https://github.com/NovusEdge/stoat";
              license = pkgs.lib.licenses.agpl3Plus;
              mainProgram = "stoat";
              # stoat shells out to qemu-system-x86_64/qemu-img and reads
              # /proc directly, so it only runs on Linux.
              platforms = pkgs.lib.platforms.linux;
            };
          };
        }
      );

      apps = eachSystem (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.system}.default}/bin/stoat";
        };
      });

      devShells = eachSystem (pkgs: {
        default = pkgs.mkShell {
          # Mirrors the binaries `just doctor` checks for.
          packages = [
            pkgs.go
            pkgs.just
            pkgs.qemu
            pkgs.openssh
          ];
        };
      });

      # NOTE on runtime deps: stoat's QEMU/OpenSSH dependencies are shelled
      # out to, not linked, so we deliberately do NOT wrapProgram the
      # `stoat` binary to force nixpkgs' qemu/openssh onto its PATH. The
      # README tells users to pick a QEMU build with GTK+OpenGL support for
      # `-display gtk,gl=on`; silently substituting a different nixpkgs
      # qemu build would risk breaking that display path in a way that's
      # surprising and hard to debug. qemu-system-x86_64/qemu-img/ssh/
      # ssh-keygen remain documented prerequisites the user supplies
      # themselves (see devShells.default for a convenient dev environment
      # that includes them).
    };
}
