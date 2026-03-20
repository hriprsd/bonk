flake:

{ config, lib, pkgs, ... }:

let
  cfg = config.programs.bonk;
  bonkPkg = flake.packages.${pkgs.stdenv.hostPlatform.system}.default;
in
{
  options.programs.bonk = {
    enable = lib.mkEnableOption "bonk - open apps by slapping the laptop";

    package = lib.mkOption {
      type = lib.types.package;
      default = bonkPkg;
      defaultText = lib.literalExpression "inputs.bonk.packages.\${system}.default";
      description = "The bonk package to use.";
    };

    mode = lib.mkOption {
      type = lib.types.enum [ "pain" "sexy" "halo" ];
      default = "pain";
      description = "Audio mode to use.";
    };

    fast = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable faster detection tuning.";
    };

    minAmplitude = lib.mkOption {
      type = lib.types.nullOr lib.types.float;
      default = null;
      description = "Minimum amplitude threshold (0.0-1.0).";
    };

    cooldown = lib.mkOption {
      type = lib.types.nullOr lib.types.int;
      default = null;
      description = "Cooldown between responses in milliseconds.";
    };

    volumeScaling = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Scale playback volume by slap amplitude.";
    };

    customPath = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Path to custom MP3 audio directory.";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];
  };
}
