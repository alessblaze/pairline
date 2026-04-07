// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

const SkipDotEnvEnvVar = "IGNORE_DOTENV"

func LoadDotEnvIfEnabled() {
	if os.Getenv(SkipDotEnvEnvVar) != "" {
		log.Printf("%s is set, skipping .env loading", SkipDotEnvEnvVar)
		return
	}

	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using default values")
	}
}
