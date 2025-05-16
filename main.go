package main

import (
	"encoding/json" // Для кодирования/декодирования JSON
	"flag"          // Для обработки флагов командной строки (наш API ключ)
	"fmt"
	"html/template" // Пакет для работы с HTML-шаблонами
	"log"           // Для логирования ошибок
	"math"          // Для math.Ceil() при расчете страниц
	"net/http"
	"net/url" // Для работы с URL и его параметрами
	"os"      // Пакет для работы с переменными окружения, например, для порта
	"strconv" // Для конвертации строки в число (page)
	"time"    // Для работы с датой и временем (поле PublishedAt)
)

// tpl будет хранить наш предварительно загруженный и проверенный шаблон.
var tpl = template.Must(template.ParseFiles("index.html"))

// apiKey будет хранить API ключ, полученный из флага командной строки.
var apiKey *string

// --- Модели данных ---
type Source struct {
	ID   interface{} `json:"id"` // interface{} потому что id может быть null или строкой
	Name string      `json:"name"`
}

type Article struct {
	Source      Source    `json:"source"`
	Author      string    `json:"author"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	URLToImage  string    `json:"urlToImage"`
	PublishedAt time.Time `json:"publishedAt"` // Тип time.Time для дат
	Content     string    `json:"content"`
}

// FormatPublishedDate форматирует дату публикации статьи для отображения.
func (a *Article) FormatPublishedDate() string {
	// time.Month.String() возвращает имя месяца (January, February, etc.)
	// a.PublishedAt.Day() возвращает день месяца
	// a.PublishedAt.Year() возвращает год
	return fmt.Sprintf("%s %d, %d", a.PublishedAt.Month().String(), a.PublishedAt.Day(), a.PublishedAt.Year())
}

type Results struct {
	Status       string    `json:"status"`
	TotalResults int       `json:"totalResults"`
	Articles     []Article `json:"articles"` // Слайс (массив) структур Article
}

// Search структура представляет данные для нашего шаблона на странице результатов поиска
type Search struct {
	SearchKey  string  // Что искали
	QueryPage  int     // Страница, запрошенная у API (текущая отображаемая)
	TotalPages int     // Всего страниц результатов
	Results    Results // Сами результаты поиска (структура Results выше)
}

// IsLastPage проверяет, является ли текущая страница последней.
func (s *Search) IsLastPage() bool {
	return s.QueryPage >= s.TotalPages
}

// NextPageLink возвращает номер следующей страницы для ссылки "Next".
func (s *Search) NextPageLink() int {
	return s.QueryPage + 1
}

// PrevPageLink возвращает номер предыдущей страницы для ссылки "Previous".
func (s *Search) PrevPageLink() int {
	return s.QueryPage - 1
}

// --- Конец моделей данных ---

// indexHandler обрабатывает запросы на главную страницу.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	// Если на главную страницу передали параметры поиска (например, нажали Enter в пустом поле),
	// то не нужно ничего отображать, кроме пустой формы.
	// Для этого передаем nil в качестве данных.
	err := tpl.Execute(w, nil)
	if err != nil {
		log.Println("Error executing template for indexHandler:", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// searchHandler обрабатывает поисковые запросы.
func searchHandler(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse(r.URL.String())
	if err != nil {
		http.Error(w, "Internal server error: URL parsing failed", http.StatusInternalServerError)
		return
	}

	params := u.Query()
	searchKey := params.Get("q")
	pageStr := params.Get("page")
	if pageStr == "" {
		pageStr = "1"
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 { // Страница не может быть не числом или меньше 1
		page = 1 // По умолчанию на первую страницу
	}

	// Создаем экземпляр структуры Search, куда будем складывать данные
	searchData := &Search{}
	searchData.SearchKey = searchKey
	searchData.QueryPage = page

	// Если поисковый ключ пуст, просто отображаем шаблон без результатов
	if searchKey == "" {
		err = tpl.Execute(w, searchData) // Передаем searchData, чтобы {{.SearchKey}} был пуст
		if err != nil {
			log.Println("Error executing template for empty search:", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	pageSize := 20 // Количество статей на странице

	// Формируем URL для запроса к News API
	endpoint := fmt.Sprintf("https://newsapi.org/v2/everything?q=%s&pageSize=%d&page=%d&apiKey=%s&sortBy=publishedAt&language=en",
		url.QueryEscape(searchData.SearchKey), pageSize, searchData.QueryPage, *apiKey)

	// Выполняем GET-запрос
	resp, err := http.Get(endpoint)
	if err != nil {
		log.Println("News API request failed:", err)
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	// Проверяем статус-код ответа
	if resp.StatusCode != http.StatusOK {
		log.Printf("News API returned non-200 status: %d for query: %s", resp.StatusCode, searchKey)
		http.Error(w, "No results or API error", http.StatusNoContent) // Может быть 204 или другая ошибка
		return
	}

	// Декодируем JSON-ответ в нашу структуру searchData.Results
	err = json.NewDecoder(resp.Body).Decode(&searchData.Results)
	if err != nil {
		log.Println("Failed to decode News API response:", err)
		http.Error(w, "Error processing news data", http.StatusInternalServerError)
		return
	}

	// Рассчитываем общее количество страниц
	if searchData.Results.TotalResults > 0 {
		searchData.TotalPages = int(math.Ceil(float64(searchData.Results.TotalResults) / float64(pageSize)))
	} else {
		searchData.TotalPages = 0
	}

	// Если запрошенная страница больше, чем общее количество страниц (и есть результаты)
	// то лучше отобразить последнюю доступную страницу или ошибку.
	// В данном случае, API сам вернет пустой список статей, если page слишком большой.
	// Но TotalPages будет корректным.
	if searchData.QueryPage > searchData.TotalPages && searchData.TotalPages > 0 {
		// Можно сделать редирект на последнюю страницу
		// http.Redirect(w, r, fmt.Sprintf("/search?q=%s&page=%d", url.QueryEscape(searchData.SearchKey), searchData.TotalPages), http.StatusSeeOther)
		// return
		// Или просто позволить шаблону отобразить "You are on page X of Y" с пустым списком
		// Пользователь сам увидит, что результатов нет.
	}

	// Отправляем данные в шаблон
	err = tpl.Execute(w, searchData)
	if err != nil {
		log.Println("Error executing template for searchHandler:", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func main() {
	// Определяем флаг командной строки "apikey"
	apiKey = flag.String("apikey", "", "Newsapi.org access key")
	flag.Parse() // Анализируем переданные флаги

	// Проверяем, был ли передан apikey
	if *apiKey == "" {
		log.Fatal("apiKey must be set. Get one from newsapi.org and run with -apikey=YOUR_KEY")
	}

	// Пытаемся получить порт из переменной окружения PORT.
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // Если переменная PORT не установлена, используем порт 3000 по умолчанию
	}

	// Создаем новый маршрутизатор (mux).
	mux := http.NewServeMux()

	// Обслуживание статических файлов из папки 'assets'
	fs := http.FileServer(http.Dir("assets"))
	mux.Handle("/assets/", http.StripPrefix("/assets/", fs))

	// Регистрируем обработчики
	mux.HandleFunc("/search", searchHandler)
	mux.HandleFunc("/", indexHandler)

	log.Println("Starting server on port :" + port)
	// Запускаем HTTP-сервер
	err := http.ListenAndServe(":"+port, mux)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
