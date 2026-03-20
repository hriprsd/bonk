{ lib, buildGoModule, go_1_26 }:

buildGoModule.override { go = go_1_26; } {
  pname = "bonk";
  version = "0-unstable";

  src = lib.cleanSource ./..;

  # Run `nix build` to get the correct hash, then replace this placeholder
  vendorHash = "sha256-R63lSTQvwZ/zw2ccuwXk6rkuQ0t2Zs4mU3Trh+IxKf4=";

  ldflags = [ "-s" "-w" ];

  # Tests require Apple Silicon accelerometer hardware
  doCheck = false;

  meta = {
    description = "Open apps by slapping the laptop";
    homepage = "https://github.com/hriprsd/bonk";
    license = lib.licenses.mit;
    platforms = [ "aarch64-darwin" ];
    mainProgram = "bonk";
  };
}
