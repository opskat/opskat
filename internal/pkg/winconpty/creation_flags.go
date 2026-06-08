package winconpty

const (
	createNoWindowFlag             uint32 = 0x08000000
	createUnicodeEnvironmentFlag   uint32 = 0x00000400
	extendedStartupInfoPresentFlag uint32 = 0x00080000
)

func processCreationFlags(hasEnv bool) uint32 {
	flags := extendedStartupInfoPresentFlag
	if hasEnv {
		flags |= createUnicodeEnvironmentFlag
	}
	return flags
}
