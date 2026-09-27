import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';

export interface PrixResult {
  resto: string;
  quartier: string;
  type: string;
  plat: string;
  prix_fcfa: number;
  portion: string;
  date_releve: string;
}

export interface Resto {
  nom: string;
  quartier: string;
  type: string;
}

// URL de base de l'API Go. Change-la si ton backend tourne ailleurs.
const API_BASE = 'http://localhost:8080';

@Injectable({ providedIn: 'root' })
export class DishService {
  constructor(private http: HttpClient) {}

  getPlats(): Observable<string[]> {
    return this.http.get<string[]>(`${API_BASE}/plats`);
  }

  getMoinsCher(nomPlat: string): Observable<PrixResult[]> {
    return this.http.get<PrixResult[]>(
      `${API_BASE}/plats/${encodeURIComponent(nomPlat)}/moins-cher`
    );
  }

  getRestos(): Observable<Resto[]> {
    return this.http.get<Resto[]>(`${API_BASE}/restos`);
  }
}
