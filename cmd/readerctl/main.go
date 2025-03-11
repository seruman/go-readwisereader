package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	"github.com/seruman/go-readwisereader"
)

type UsageError error

var ErrorUsage = UsageError(errors.New("usage"))

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	stderr := os.Stderr
	stdout := os.Stdout

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	defaultConfigPath := fmt.Sprintf("%s/.config/readerctl/config", home)

	var rootopts RootOpts
	rootfs := ff.NewFlagSet(args[0])
	rootfs.StringVar(&rootopts.APIToken, 't', "api-token", "", "API token")
	_ = rootfs.String('c', "config", defaultConfigPath, "config file")

	withRootFlags := func(f func(context.Context, []string) error) func(context.Context, []string) error {
		return func(ctx context.Context, args []string) error {
			if err := rootopts.validate(); err != nil {
				return fmt.Errorf("%w: %v", ErrorUsage, err)
			}

			return f(ctx, args)
		}
	}

	var listopts ListOpts
	listfs := ff.NewFlagSet("list").SetParent(rootfs)
	listfs.BoolVarDefault(&listopts.Paginate, 'p', "paginate", false, "paginate results")
	listcmd := &ff.Command{
		Name:  "list",
		Flags: listfs,
		Exec: withRootFlags(func(ctx context.Context, args []string) error {
			client := rootopts.client()
			it := client.ListPaginate(ctx, readwisereader.ListParams{})

			for page, err := range it {
				if err != nil {
					return err
				}

				for _, article := range page.Results {
					fmt.Fprintf(stdout, "Article %s:\n%s\n\n", article.ID, article.Title)
				}

			}

			return nil
		}),
	}

	savefs := ff.NewFlagSet("create").SetParent(rootfs)
	savecmd := &ff.Command{
		Name:  "save",
		Flags: savefs,
		Usage: "readerctl save [flags] <url>",
		Exec: withRootFlags(func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: expected exactly 1 argument", ErrorUsage)
			}

			client := rootopts.client()

			saveddoc, err := client.Save(
				ctx, readwisereader.SaveParams{
					URL: args[0],
				})
			if err != nil {
				return err
			}

			fmt.Fprintf(stdout, "Saved document %s: %s\n", saveddoc.ID, saveddoc.URL)

			return nil
		}),
	}

	deletefs := ff.NewFlagSet("delete").SetParent(rootfs)
	deletecmd := &ff.Command{
		Name:      "delete",
		Flags:     deletefs,
		Usage:     "readerctl delete [flags] <article-id>",
		ShortHelp: "Delete an article from Readwise Reader",
		LongHelp:  "Delete an article from Readwise Reader using the article ID.",
		Exec: withRootFlags(func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: expected exactly 1 argument", ErrorUsage)
			}

			client := rootopts.client()

			err := client.Delete(ctx, args[0])
			if err != nil {
				return err
			}

			fmt.Fprintf(stdout, "Deleted article %s\n", args[0])

			return nil
		}),
	}

	rootcmd := &ff.Command{
		Name:  args[0],
		Flags: rootfs,
		Subcommands: []*ff.Command{
			listcmd,
			savecmd,
			deletecmd,
		},
		Exec: func(ctx context.Context, args []string) error {
			return ff.ErrHelp
		},
	}

	err = rootcmd.Parse(
		args[1:],
		ff.WithEnvVarPrefix("READERCTL"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
		ff.WithConfigAllowMissingFile(),
	)
	switch {
	case errors.Is(err, ff.ErrHelp):
		fmt.Fprintf(stderr, "%s\n", ffhelp.Command(rootcmd))
		return nil

	case errors.Is(err, ErrorUsage):
		fmt.Fprintf(stderr, "%s\n", ffhelp.Command(rootcmd))
	}

	err = rootcmd.Run(ctx)
	switch {
	case errors.Is(err, ff.ErrHelp):
		fmt.Fprintf(stderr, "%s\n", ffhelp.Command(rootcmd))
		return nil

	case errors.Is(err, ErrorUsage):
		fmt.Fprintf(stderr, "%s\n", ffhelp.Command(rootcmd))
	}

	return err
}

type RootOpts struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	APIToken string
}

func (o *RootOpts) validate() error {
	if o.APIToken == "" {
		return fmt.Errorf("api token is required")
	}

	return nil
}

func (o *RootOpts) client() *readwisereader.Client {
	return readwisereader.NewClient(o.APIToken)
}

type ListOpts struct {
	Paginate bool
}
