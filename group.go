package main

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/twmb/kgo/kerr"
	"github.com/twmb/kgo/kmsg"
)

func init() {
	root.AddCommand(groupCmd())
}

func groupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Perform group related actions",
	}

	cmd.AddCommand(groupListCmd())
	cmd.AddCommand(groupDescribeCmd())
	cmd.AddCommand(groupDeleteCmd())

	return cmd
}

func groupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all groups",
		Long: `List all Kafka groups.

This command simply lists groups and their protocol types; it does not describe
the groups listed. This is all of the information from the ListGroups request.

To get a lot more information about groups, use the describe command.
`,
		Args: cobra.ExactArgs(0),
		Run: func(_ *cobra.Command, _ []string) {
			kresp, err := client().Request(new(kmsg.ListGroupsRequest))
			maybeDie(err, "unable to list groups: %v", err)
			resp := kresp.(*kmsg.ListGroupsResponse)

			if asJSON {
				dumpJSON(kresp)
				return
			}

			if err = kerr.ErrorForCode(resp.ErrorCode); err != nil {
				die("%s", err)
				return
			}

			fmt.Fprintf(tw, "GROUP ID\tPROTO TYPE\n")
			for _, group := range resp.Groups {
				fmt.Fprintf(tw, "%s\t%s\n", group.GroupID, group.ProtocolType)
			}
			tw.Flush()
		},
	}
}

func groupDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe GROUPS...",
		Short: "Describe Kafka groups",
		Long: `Describe Kafka groups.

Describe Kafka groups. If only one group is listed, this gives detailed
info on the single group.

This command supports JSON output.
`,
		Args: cobra.MinimumNArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			req := kmsg.DescribeGroupsRequest{
				GroupIDs: args,
			}

			kresp, err := client().Request(&req)
			maybeDie(err, "unable to describe groups: %v", err)
			resp := unbinaryGroupDescribeMembers(kresp.(*kmsg.DescribeGroupsResponse))

			if asJSON {
				dumpJSON(resp)
				return
			}

			if len(args) == 1 {
				describeGroupDetailed(&resp.Groups[0])
				return
			}

			fmt.Fprintf(tw, "GROUP ID\tSTATE\tPROTO TYPE\tPROTO\tERROR\n")
			for _, group := range resp.Groups {
				errMsg := ""
				if err := kerr.ErrorForCode(group.ErrorCode); err != nil {
					errMsg = err.Error()
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					group.GroupID,
					group.State,
					group.ProtocolType,
					group.Protocol,
					errMsg,
				)
			}
			tw.Flush()
		},
	}
}

func describeGroupDetailed(group *consumerGroup) {
	if err := kerr.ErrorForCode(group.ErrorCode); err != nil {
		die("%s", err)
	}

	fmt.Fprintf(tw, "ID\t%s\n", group.GroupID)
	fmt.Fprintf(tw, "STATE\t%s\n", group.State)
	fmt.Fprintf(tw, "PROTO TYPE\t%s\n", group.ProtocolType)
	fmt.Fprintf(tw, "PROTO\t%s\n", group.Protocol)
	fmt.Fprintf(tw, "MEMBERS\t%d\n", len(group.Members))
	tw.Flush()
	fmt.Println()

	// TODO watermarks?

	sb := new(strings.Builder)
	groupTW := tabwriter.NewWriter(sb, 6, 4, 2, ' ', 0)
	fmt.Fprintf(groupTW, "MEMBER ID\tCLIENT ID\tCLIENT HOST\tUSER DATA\n")

	for _, member := range group.Members {
		fmt.Fprintf(groupTW, "%s\t%s\t%s\t%s\n",
			member.MemberID,
			member.ClientID,
			member.ClientHost,
			string(member.MemberAssignment.UserData))
	}
	groupTW.Flush()

	lines := strings.Split(sb.String(), "\n")
	lines = lines[:len(lines)-1] // trim trailing empty line
	fmt.Println(lines[0])

	for i, line := range lines[1:] {
		fmt.Println(line)

		member := group.Members[i]
		assignment := &member.MemberAssignment
		if len(assignment.Topics) == 0 {
			continue
		}

		sort.Slice(assignment.Topics, func(i, j int) bool {
			return assignment.Topics[i].Topic < assignment.Topics[j].Topic
		})

		fmt.Println("\tASSIGNMENTS")
		for _, topic := range assignment.Topics {
			sort.Slice(topic.Partitions, func(i, j int) bool { return topic.Partitions[i] < topic.Partitions[j] })
			fmt.Printf("\t%s => %v\n", topic.Topic, topic.Partitions)
		}
	}
}

type consumerGroupMember struct {
	MemberID         string
	ClientID         string
	ClientHost       string
	MemberMetadata   kmsg.GroupMemberMetadata
	MemberAssignment kmsg.GroupMemberAssignment
}
type consumerGroup struct {
	ErrorCode            int16
	GroupID              string
	State                string
	ProtocolType         string
	Protocol             string
	Members              []consumerGroupMember
	AuthorizedOperations int32
}
type consumerGroups struct {
	ThrottleTimeMs int32
	Groups         []consumerGroup
}

// unmarshals and prints the standard java protocol type
func unbinaryGroupDescribeMembers(resp *kmsg.DescribeGroupsResponse) *consumerGroups {
	cresp := &consumerGroups{
		ThrottleTimeMs: resp.ThrottleTimeMs,
	}
	for _, group := range resp.Groups {
		cgroup := consumerGroup{
			ErrorCode:            group.ErrorCode,
			GroupID:              group.GroupID,
			State:                group.State,
			ProtocolType:         group.ProtocolType,
			Protocol:             group.Protocol,
			AuthorizedOperations: group.AuthorizedOperations,
		}
		for _, member := range group.Members {
			cmember := consumerGroupMember{
				MemberID:   member.MemberID,
				ClientID:   member.ClientID,
				ClientHost: member.ClientHost,
			}
			cmember.MemberMetadata.ReadFrom(member.MemberMetadata)
			cmember.MemberAssignment.ReadFrom(member.MemberAssignment)

			cgroup.Members = append(cgroup.Members, cmember)
		}
		cresp.Groups = append(cresp.Groups, cgroup)
	}

	return cresp
}

func groupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete GROUPS...",
		Short: "Delete all listed Kafka groups",
		Args:  cobra.MinimumNArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			kresp, err := client().Request(&kmsg.DeleteGroupsRequest{
				Groups: args,
			})
			maybeDie(err, "unable to delete groups: %v", err)
			resp := kresp.(*kmsg.DeleteGroupsResponse)
			if asJSON {
				dumpJSON(resp)
				return
			}
			for _, resp := range resp.GroupErrorCodes {
				msg := "OK"
				if err := kerr.ErrorForCode(resp.ErrorCode); err != nil {
					msg = err.Error()
				}
				fmt.Fprintf(tw, "%s\t%s\n", resp.GroupID, msg)
			}
			tw.Flush()
		},
	}
}
