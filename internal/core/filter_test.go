package core

import (
	"testing"
	"time"
)

func targetPosting(company, title string) Posting {
	return Posting{Company: company, Title: title, Country: "US", Location: "New York, NY"}
}

func TestEligible(t *testing.T) {
	tests := []struct {
		name    string
		posting Posting
		want    bool
	}{
		{"software intern", targetPosting("OpenAI", "Software Engineer Intern"), true},
		{"backend new grad", targetPosting("Stripe", "Backend Engineer, New Grad"), true},
		{"frontend graduate", targetPosting("Figma", "Graduate Front-End Developer"), true},
		{"platform early career", targetPosting("Cloudflare", "Platform Engineer, Early Career"), true},
		{"swe intern", targetPosting("Google", "SWE Intern"), true},
		{"backend engineering co-op", targetPosting("Stripe", "Backend Engineering Co-op"), true},
		{"ml internship via employment type", Posting{Company: "Anthropic", Title: "Machine Learning Engineer", EmploymentType: "Internship", Country: "SG"}, true},
		{"prose alone does not establish timing", Posting{Company: "Anthropic", Title: "Machine Learning Engineer", Description: "You will mentor interns.", Country: "US"}, false},
		{"coding llms research intern", targetPosting("Excellent AI", "Research Intern — Coding LLMs"), true},
		{"quantitative researcher intern", targetPosting("Jane Street", "Quantitative Researcher Internship"), true},
		{"quantitative trading analyst intern", targetPosting("DRW", "Quantitative Trading Analyst Intern"), true},
		{"quantitative analyst phd intern", targetPosting("D. E. Shaw", "Quantitative Analyst, Ph.D. Intern - Summer 2027"), true},
		{"d e shaw exact live payload", Posting{Company: "D. E. Shaw", Title: "Quantitative Analyst, Ph.D. Intern (New York) – Summer 2027", Location: "New York, NY, United States", Country: "United States", Level: "internship"}, true},
		{"quantitative trading intern", targetPosting("Belvedere", "Quantitative Trading Intern"), true},
		{"quantitative technologist intern", targetPosting("Radix Trading", "Quantitative Technologist (C++ Intern)"), true},
		{"quantitative technologist source scoped", Posting{Company: "Radix Trading", Title: "Quantitative Technologist (Full-Time - C++ Developer)", EmploymentType: "full_time", Level: "early career", Country: "US"}, true},
		{"data science intern", targetPosting("Databricks", "Data Science Intern"), true},
		{"plural data engineers intern", targetPosting("IBM", "Intern Data Engineers - AI & Analytics - 2027"), true},
		{"security new grad", targetPosting("Cloudflare", "Application Security Engineer, New Grad"), true},
		{"ai research scientist", targetPosting("OpenAI", "Research Scientist Intern — AI"), true},
		{"applied vision research scientist", targetPosting("Meta", "Research Scientist Intern, Applied Vision"), true},
		{"nlp applied research", targetPosting("NVIDIA", "Applied Research Intern, NLP"), true},
		{"ai ml research intern", targetPosting("DRW", "AI/ML Research Intern"), true},
		{"ai agent engineering intern", targetPosting("ByteDance", "AI Agent Engineering Project Intern"), true},
		{"research engineer intern", targetPosting("Anthropic", "Research Engineer Intern"), true},
		{"production engineer intern", targetPosting("ByteDance", "Production Engineer Intern"), true},
		{"systems engineer campus", targetPosting("Jump Trading", "Campus Systems Engineer"), true},
		{"algorithm development internship", targetPosting("HRT", "Algorithm Development Internship"), true},
		{"linux engineer new grad", targetPosting("Jane Street", "Linux Engineer, New Grad"), true},
		{"network engineer intern", targetPosting("Jane Street", "Network Engineer Intern"), true},
		{"generic research internship remains nontechnical", targetPosting("Cohere", "Research Internship (Fall 2026)"), false},
		{"ai research internship", targetPosting("Cohere", "AI Research Internship (Fall 2026)"), true},
		{"genai research scientist", targetPosting("Databricks", "PhD GenAI Research Scientist Intern"), true},
		{"privacy engineering new grad", targetPosting("Palantir", "Privacy & Civil Liberties Engineer - New Grad"), true},
		{"member of technical staff new grad", targetPosting("OpenAI", "Member of Technical Staff, New Grad"), true},
		{"mts intern", targetPosting("Excellent AI", "MTS Intern"), true},
		{"data management remains technical", targetPosting("ByteDance", "Backend Software Engineer Intern, Data Management Suite"), true},
		{"core operations engineer remains technical", targetPosting("Virtu", "2027 Internship - Core Operations Engineer"), true},
		{"trading operations support is nontechnical", targetPosting("Five Rings", "Campus Full Time 2027 - Trading Operations Engineer"), false},
		{"kernel engineer new grad", targetPosting("Cerebras", "Kernel Engineer - New Grad"), true},
		{"graduate developer", targetPosting("Maven Securities", "Graduate Developer Programme Chicago 2027"), true},
		{"application developer intern", targetPosting("IBM", "Intern Application Developer - Oracle - 2027"), true},
		{"college grad software engineering", targetPosting("Salesforce", "Software Engineering AMTS (College Grad)"), true},
		{"ai builder intern", targetPosting("Scale AI", "AI Builder Intern"), true},
		{"model shaping research intern", targetPosting("Together AI", "Research Intern, Model Shaping"), true},
		{"title geography fallback", Posting{Company: "Cloudflare", Title: "Software Engineer Intern - Austin, TX", Location: "In-Office"}, true},
		{"available locations austin", Posting{Company: "Cloudflare", Title: "Research Engineer Intern (Fall 2026)", Location: "Austin, TX"}, true},
		{"available locations lisbon", Posting{Company: "Cloudflare", Title: "Research Engineer Intern (Fall 2026)", Location: "Lisbon, Portugal"}, false},
		{"available locations london", Posting{Company: "Cloudflare", Title: "Research Engineer Intern (Fall 2026)", Location: "London, United Kingdom"}, false},
		{"business developer remains nontechnical", targetPosting("Example", "Business Developer Intern"), false},
		{"content developer remains nontechnical", targetPosting("Example", "Content Developer Intern"), false},
		{"handshake internship guide is editorial content", Posting{Company: "Handshake", Title: "Your Guide to Software Engineering Internships", Location: "San Francisco, CA, United States", Country: "United States", Level: "internship", ApplyURL: "https://joinhandshake.com/blog/students/software-engineering-internships"}, false},
		{"editorial blog URL cannot masquerade as a role", Posting{Company: "Example", Title: "Software Engineer Intern", Location: "San Francisco, CA", Country: "US", ApplyURL: "https://example.com/blog/software-engineer-intern"}, false},
		{"engineering guides team remains eligible on a job URL", Posting{Company: "Example", Title: "Software Engineer Intern, Developer Guides", Location: "San Francisco, CA", Country: "US", ApplyURL: "https://jobs.example.com/openings/123"}, true},
		{"ux research intern remains nontechnical", targetPosting("Example", "UX Research Intern"), false},
		{"ux research internship remains nontechnical", targetPosting("Example", "UX Research Internship"), false},
		{"user research internship remains nontechnical", targetPosting("Example", "User Research Internship"), false},
		{"wechat product title", targetPosting("Example", "WeChat Backend Engineer Intern"), false},
		{"unknown timing", targetPosting("Vercel", "Software Engineer, Deployment Infrastructure"), false},
		{"explicit experienced beats inferred level", Posting{Company: "Akuna", Title: "Platform Engineer", EmploymentType: "Experienced Platform", Level: "internship", Country: "US"}, false},
		{"explicit intern title beats bad experienced metadata", Posting{Company: "Akuna", Title: "Platform Engineer Intern", EmploymentType: "Experienced Platform", Level: "internship", Country: "US"}, true},
		{"nontechnical engineering", targetPosting("RTX", "Methods Intern - Hot Section Engineering"), false},
		{"support", targetPosting("Rippling", "Customer Support Specialist Intern"), false},
		{"it operations", targetPosting("Jane Street", "IT Operations Engineer Intern"), false},
		{"business analyst", targetPosting("Adobe", "Business Analyst Intern"), false},
		{"generic financial analyst", targetPosting("Example", "Financial Analyst Intern"), false},
		{"security analyst", targetPosting("Jane Street", "Cybersecurity Analyst, New Grad"), false},
		{"recruiter", targetPosting("Jane Street", "Campus Recruiter, Machine Learning and Quantitative Research"), false},
		{"educator", targetPosting("Jane Street", "Machine Learning Educator, New Grad"), false},
		{"virtual challenge", targetPosting("Akuna", "2027 Virtual Quant Trading Challenge"), false},
		{"government", targetPosting("Palantir", "Software Engineer, New Grad - US Government"), false},
		{"defense", targetPosting("Palantir", "Software Engineer Internship - Defense Tech"), false},
		{"palantir intel lane", targetPosting("Palantir", "Forward Deployed Software Engineer, Internship - Intel"), false},
		{"engineering manager", targetPosting("Rippling", "Software Engineering Manager, Early Career Products"), false},
		{"director", targetPosting("Vercel", "Director of Software Engineering, Graduate Programs"), false},
		{"head", targetPosting("Vercel", "Head, Software Engineering Internships"), false},
		{"senior", targetPosting("Brex", "Senior Software Engineer, University Graduate"), false},
		{"staff", targetPosting("Databricks", "Staff Machine Learning Engineer, Early Career"), false},
		{"principal", targetPosting("Amazon", "Principal Software Engineer Intern"), false},
		{"qa software engineer", targetPosting("Adobe", "QA Software Engineer Intern"), false},
		{"sdet", targetPosting("Amazon", "SDET Intern"), false},
		{"software engineer in test", targetPosting("Stripe", "Software Engineer in Test, New Grad"), false},
		{"software test automation", targetPosting("Google", "Software Engineer, Test Automation Intern"), false},
		{"test engineer", targetPosting("Meta", "Software Test Engineer Intern"), false},
		{"hardware", targetPosting("IMC", "Hardware Engineer Intern"), false},
		{"tencent company", targetPosting("Tencent", "Backend Engineer Intern"), false},
		{"wechat brand", targetPosting("WeChat", "Software Engineer Intern"), false},
		{"anduril defense company", targetPosting("Anduril Industries", "Early Career Software Engineer 2027"), false},
		{"raytheon defense company", targetPosting("Raytheon", "Software Engineer Intern"), false},
		{"rtx defense company", targetPosting("RTX", "Software Engineer Intern"), false},
		{"high signal company remains eligible", targetPosting("Airwallex", "Software Engineer Intern 2027"), true},
		{"staffing is not staff", targetPosting("Staffing.com", "Software Engineer Intern"), true},
		{"singapore country", Posting{Company: "Jane Street", Title: "Software Engineer Intern", Country: "Singapore"}, true},
		{"sf office fallback", Posting{Company: "Abridge", Title: "Software Engineer Intern", Location: "SF Office"}, true},
		{"us state fallback", Posting{Company: "Figma", Title: "Software Engineer Intern", Location: "San Francisco, CA"}, true},
		{"washington dc fallback", Posting{Company: "Palantir", Title: "Software Engineer Intern", Location: "Washington, D.C."}, true},
		{"foreign title contradicts us location", Posting{Company: "Palantir", Title: "Forward Deployed Software Engineer, Internship - France", Country: "US", Location: "New York, NY"}, false},
		{"multi-region title retains target", Posting{Company: "Example", Title: "Software Engineer Intern - US and Canada", Location: "Remote"}, true},
		{"title target does not override foreign location", Posting{Company: "Example", Title: "Software Engineer Intern - Austin Team", Location: "London, United Kingdom"}, false},
		{"target country does not override foreign location", Posting{Company: "Example", Title: "Software Engineer Intern", Country: "US", Location: "London, United Kingdom"}, false},
		{"generic word does not erase foreign location", Posting{Company: "Example", Title: "Software Engineer Intern - Austin Team", Location: "Remote - London, United Kingdom"}, false},
		{"country prefix is not a US state", Posting{Company: "Snowflake", Title: "Software Engineer Intern", Location: "DE-Berlin-Trion Building"}, false},
		{"multi location target", Posting{Company: "HRT", Title: "Software Engineering Internship", Location: "London; New York; Singapore"}, true},
		{"london only", Posting{Company: "Jane Street", Title: "Software Engineer Intern", Country: "UK", Location: "London, United Kingdom"}, false},
		{"hong kong only", Posting{Company: "Jane Street", Title: "Software Engineer Intern", Country: "Hong Kong", Location: "Hong Kong"}, false},
		{"generic remote", Posting{Company: "Akuna", Title: "Software Engineer Intern", Location: "Remote"}, false},
		{"generic hybrid", Posting{Company: "Vercel", Title: "Software Engineer Intern", Location: "Hybrid"}, false},
		{"empty geography", Posting{Company: "OpenAI", Title: "Software Engineer Intern"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Eligible(test.posting); got != test.want {
				t.Fatalf("Eligible(%#v) = %v, want %v", test.posting, got, test.want)
			}
		})
	}
}

func TestEligibleAtRejectsExplicitlyStaleTiming(t *testing.T) {
	posting := targetPosting("Netflix", "Software Engineer PhD Intern, Streaming Algorithms (Summer 2026)")
	if EligibleAt(posting, time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC)) != true {
		t.Fatal("current summer role should remain eligible before the August cutoff")
	}
	if EligibleAt(posting, time.Date(2026, time.August, 17, 0, 0, 0, 0, time.UTC)) != false {
		t.Fatal("current summer role should be stale after the August cutoff")
	}
	future := targetPosting("Netflix", "Software Engineer Intern (Summer 2027)")
	if EligibleAt(future, time.Date(2026, time.August, 17, 0, 0, 0, 0, time.UTC)) != true {
		t.Fatal("future summer role must remain eligible")
	}
}

func TestEligibleAtRejectsOldExplicitPostedAt(t *testing.T) {
	now := time.Date(2026, time.August, 18, 0, 0, 0, 0, time.UTC)
	old := now.Add(-maxExplicitPostingAge - time.Second)
	recent := now.Add(-maxExplicitPostingAge)

	posting := targetPosting("Palantir", "Software Engineer, New Grad")
	posting.PostedAt = &old
	if EligibleAt(posting, now) {
		t.Fatal("posting older than the explicit freshness window must be rejected")
	}
	posting.PostedAt = &recent
	if !EligibleAt(posting, now) {
		t.Fatal("posting at the freshness boundary should remain eligible")
	}
	posting.PostedAt = nil
	if !EligibleAt(posting, now) {
		t.Fatal("missing posted_at must not be treated as proof of staleness")
	}
}
