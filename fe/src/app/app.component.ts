import { CommonModule } from '@angular/common';
import { Component, OnInit } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { DishService, PrixResult } from './dish.service';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './app.component.html',
  styleUrl: './app.component.css'
})
export class AppComponent implements OnInit {
  plats: string[] = [];
  platChoisi = '';
  resultats: PrixResult[] = [];

  chargementPlats = true;
  chargementResultats = false;
  erreur = '';

  constructor(private dishService: DishService) {}

  ngOnInit(): void {
    this.dishService.getPlats().subscribe({
      next: (plats) => {
        this.plats = plats;
        this.chargementPlats = false;
      },
      error: () => {
        this.erreur = "Impossible de contacter le serveur. Vérifie qu'il tourne bien sur localhost:8080.";
        this.chargementPlats = false;
      }
    });
  }

  chercher(nomPlat: string): void {
    if (!nomPlat) {
      return;
    }
    this.platChoisi = nomPlat;
    this.erreur = '';
    this.chargementResultats = true;
    this.resultats = [];

    this.dishService.getMoinsCher(nomPlat).subscribe({
      next: (res) => {
        this.resultats = res;
        this.chargementResultats = false;
      },
      error: (err) => {
        this.chargementResultats = false;
        if (err.status === 404) {
          this.erreur = `Aucun resto trouvé pour "${nomPlat}".`;
        } else {
          this.erreur = 'Une erreur est survenue pendant la recherche.';
        }
      }
    });
  }

  onRecherche(valeur: string): void {
    this.chercher(valeur.trim());
  }
}
