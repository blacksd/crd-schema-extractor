{
  description = "crd-schema-extractor - Automated CRD JSON Schema extraction pipeline";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f {
        pkgs = nixpkgs.legacyPackages.${system};
      });
    in
    {
      packages = forAllSystems ({ pkgs }: {
        default = let
          version = "0.1.0";
        in pkgs.buildGoModule {
          pname = "crd-schema-extractor";
          inherit version;
          src = ./.;
          vendorHash = "sha256-RjjtrUeDgCeEyh/mJVavLRqrz4r6F2SprAcbzcq5F6k=";
          subPackages = [ "cmd/extract" ];
          ldflags = [
            "-s" "-w"
            "-X main.version=${version}"
            "-X main.commit=${self.shortRev or "dirty"}"
            "-X main.date=1970-01-01T00:00:00Z"
          ];
          postInstall = ''
            mv $out/bin/extract $out/bin/crd-schema-extractor
          '';
        };
      });

      devShells = forAllSystems ({ pkgs }: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.check-jsonschema
            pkgs.yq-go
            pkgs.goreleaser
          ];
        };
      });
    };
}
