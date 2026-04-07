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

package main

import (
	"log"
	"os"

	"github.com/anish/omegle/backend/golang/internal/config"
	"github.com/anish/omegle/backend/golang/internal/server"
)

func main() {
	config.LoadDotEnvIfEnabled()

	svc := server.NewAdminServer()

	host := os.Getenv("HOST")
	if host == "" {
		if os.Getenv("ENABLE_IPV6") == "true" {
			host = "::"
		} else {
			host = "0.0.0.0"
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	log.Printf("Omegle Go Admin Service listening on %s\n", host+":"+port)
	if err := svc.Run(host + ":" + port); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
