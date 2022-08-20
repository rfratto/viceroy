group "default" {
  targets = ["viceroy-amd64", "viceroy-arm64"]
}

// This should be set from environment variables.
variable "OSXCROSS_SDK_URL" {}

target "viceroy-amd64" {
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  tags       = ["rfratto/viceroy:latest-amd64"]
  output     = ["type=docker"]

  args = {
    OSXCROSS_SDK_URL = "${OSXCROSS_SDK_URL}"
  }
}

target "viceroy-arm64" {
  dockerfile = "Dockerfile"
  platforms  = ["linux/arm64"]
  tags       = ["rfratto/viceroy:latest-arm64"]
  output     = ["type=docker"]

  args = {
    OSXCROSS_SDK_URL = "${OSXCROSS_SDK_URL}"
  }
}
