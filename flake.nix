{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  };
  outputs = {nixpkgs, ...}: {
    devShells = nixpkgs.lib.genAttrs ["x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin"] (
      system: let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            (_: prev: {
              golangci-lint = prev.golangci-lint.overrideAttrs (old: rec {
                version = "2.1.2";
                src = prev.fetchFromGitHub {
                  owner = "golangci";
                  repo = "golangci-lint";
                  rev = "v${version}";
                  hash = "sha256-CAO+oo3l3mlZIiC1Srhc0EfZffQOHvVkamPHzSKRSFw=";
                };
                vendorHash = "sha256-2GQp/sgYRlDengx8uy3zzqi9hwHh4CQUHoj1zaxNNLE=";
              });
            })
          ];
        };
      in {
        default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            golangci-lint
          ];
        };
      }
    );
  };
}
