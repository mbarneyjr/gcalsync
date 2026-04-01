{
  description = "gcalsync - sync Google Calendar events across multiple accounts";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      perSystem =
        { pkgs, ... }:
        {
          packages.default = pkgs.buildGoModule {
            pname = "gcalsync";
            version = "0.0.0";
            src = ./.;
            vendorHash = "sha256-O0xXpc0YDgUcueFCnFUCiLZ6Q/87IpxdZluwK82kLKg=";
          };

          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              gotools
            ];
          };
        };
    };
}
